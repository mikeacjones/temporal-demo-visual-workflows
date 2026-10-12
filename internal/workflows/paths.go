package workflows

import (
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/flow"
)

type edgeState int

const (
	undecided edgeState = iota
	taken
	notTaken
)

type stepResult struct {
	NodeID string
	Handle string
	Output any
	Err    error
}

// runPath runs one path of the flow (the main path, or one invocation of a
// lane) from its entry step to the end.
//
// A step runs once every edge into it is decided. If at least one of those
// edges was taken it runs; if none were (every upstream branch went another
// way), it is skipped and its own edges are marked not taken. That gives
// branches and joins without extra step types. First of is the exception: it
// fires on the first edge taken and cancels the branches that lost.
func (r *flowRun) runPath(ctx workflow.Context, entry flow.Node, entryOutput any) error {
	members := r.def.Reachable(entry.ID)
	now := workflow.Now(ctx)

	// A lane runs again for every message, so its steps start fresh.
	for _, id := range r.order {
		if members[id] && id != entry.ID {
			r.set(id, func(ns *flow.NodeState) { *ns = flow.NodeState{Status: flow.NodePending} })
		}
	}
	r.set(entry.ID, func(ns *flow.NodeState) {
		ns.Status, ns.StartedAt, ns.EndedAt, ns.Handle = flow.NodeCompleted, &now, &now, flow.HandleOut
		ns.Invocations++
	})
	r.complete(entry, flow.HandleOut, entryOutput)

	edges := map[string]edgeState{}
	takenOrder := map[string]int{} // when each edge was taken, for races
	decide := func(from, handle string) {
		for _, e := range r.outgoing[from] {
			if e.Handle() == handle {
				edges[e.ID] = taken
				takenOrder[e.ID] = len(takenOrder) + 1
			} else {
				edges[e.ID] = notTaken
			}
		}
	}
	decide(entry.ID, flow.HandleOut)

	results := workflow.NewBufferedChannel(ctx, len(members))
	pathCtx, cancelPath := workflow.WithCancel(ctx)
	defer cancelPath()
	cancels := map[string]workflow.CancelFunc{}
	lostTo := map[string]string{} // step -> label of the branch that won the race
	running := 0
	var failure error

	skip := func(id, reason string) {
		end := workflow.Now(ctx)
		r.set(id, func(ns *flow.NodeState) {
			ns.Status, ns.Waiting, ns.Error = flow.NodeSkipped, nil, reason
			if ns.StartedAt != nil {
				ns.EndedAt = &end
			}
		})
		for _, e := range r.outgoing[id] {
			edges[e.ID] = notTaken
		}
	}

	// race completes a First of step and cancels every branch that only fed
	// it through a losing edge.
	race := func(node flow.Node) {
		var winner flow.Edge
		for _, e := range r.incoming[node.ID] {
			if edges[e.ID] == taken && (winner.ID == "" || takenOrder[e.ID] < takenOrder[winner.ID]) {
				winner = e
			}
		}
		keep := r.ancestors(winner.Source, members)
		keep[winner.Source] = true
		losers := map[string]bool{}
		for _, e := range r.incoming[node.ID] {
			if e.ID == winner.ID {
				continue
			}
			for id := range r.ancestors(e.Source, members) {
				losers[id] = true
			}
			losers[e.Source] = true
		}
		winnerLabel := r.nodes[winner.Source].Label
		for _, id := range r.order {
			if !losers[id] || keep[id] || id == entry.ID {
				continue
			}
			switch r.state.Nodes[id].Status {
			case flow.NodeRunning, flow.NodeWaiting:
				lostTo[id] = winnerLabel
				cancels[id]()
			case flow.NodePending:
				skip(id, "lost the race to "+winnerLabel)
			}
		}
		at := workflow.Now(ctx)
		r.set(node.ID, func(ns *flow.NodeState) { ns.Status, ns.StartedAt, ns.EndedAt = flow.NodeCompleted, &at, &at })
		winnerKey := r.nodes[winner.Source].Key
		r.complete(node, flow.HandleOut, map[string]any{"winner": winnerKey, "output": r.steps[winnerKey]})
		decide(node.ID, flow.HandleOut)
	}

	launchReady := func() {
		for changed := true; changed; {
			changed = false
			for _, id := range r.order {
				if !members[id] || r.state.Nodes[id].Status != flow.NodePending {
					continue
				}
				node := r.nodes[id]
				decided, anyTaken := true, false
				for _, e := range r.incoming[id] {
					switch edges[e.ID] {
					case undecided:
						decided = false
					case taken:
						anyTaken = true
					}
				}
				if node.Type == flow.TypeFirstOf && anyTaken {
					race(node)
					changed = true
					continue
				}
				if !decided {
					continue
				}
				if !anyTaken {
					skip(id, "")
					changed = true
					continue
				}

				routesErrors := false
				for _, e := range r.outgoing[id] {
					routesErrors = routesErrors || e.Handle() == flow.HandleError
				}
				env := r.env()
				start := workflow.Now(ctx)
				r.set(id, func(ns *flow.NodeState) { ns.Status, ns.StartedAt = flow.NodeRunning, &start })
				stepCtx, cancel := workflow.WithCancel(pathCtx)
				cancels[id] = cancel
				running++
				workflow.Go(stepCtx, func(ctx workflow.Context) {
					handle, output, err := r.runNode(ctx, node, env, routesErrors, entry.ID)
					results.Send(ctx, stepResult{NodeID: node.ID, Handle: handle, Output: output, Err: err})
				})
			}
		}
	}

	launchReady()
	for running > 0 {
		var res stepResult
		results.Receive(ctx, &res)
		running--
		delete(cancels, res.NodeID)
		node := r.nodes[res.NodeID]
		end := workflow.Now(ctx)

		if res.Err != nil {
			if winner, lost := lostTo[node.ID]; lost && temporal.IsCanceledError(res.Err) {
				skip(node.ID, "lost the race to "+winner)
				if failure == nil {
					launchReady()
				}
				continue
			}
			if temporal.IsCanceledError(res.Err) && (failure != nil || ctx.Err() != nil) {
				reason := "stopped because another step failed"
				if ctx.Err() != nil {
					reason = "run canceled"
				}
				skip(node.ID, reason)
				if failure == nil {
					failure = res.Err
				}
				continue
			}
			r.set(node.ID, func(ns *flow.NodeState) {
				ns.Status, ns.EndedAt, ns.Waiting, ns.Error = flow.NodeFailed, &end, nil, describeError(res.Err)
			})
			if failure == nil {
				failure = res.Err
				cancelPath()
			}
			continue
		}

		r.set(node.ID, func(ns *flow.NodeState) {
			ns.Status, ns.EndedAt, ns.Waiting, ns.Handle = flow.NodeCompleted, &end, nil, res.Handle
		})
		r.complete(node, res.Handle, res.Output)
		decide(node.ID, res.Handle)
		if failure == nil {
			launchReady()
		}
	}

	if failure != nil {
		for _, id := range r.order {
			if members[id] && r.state.Nodes[id].Status == flow.NodePending {
				skip(id, "not reached")
			}
		}
	}
	return failure
}

// complete records a step's output where later steps can read it.
func (r *flowRun) complete(node flow.Node, handle string, output any) {
	value, raw := normalize(output)
	r.steps[node.Key] = value
	r.set(node.ID, func(ns *flow.NodeState) { ns.Handle, ns.Output = handle, raw })
}

// ancestors returns the steps upstream of id within one path.
func (r *flowRun) ancestors(id string, members map[string]bool) map[string]bool {
	seen := map[string]bool{}
	stack := []string{id}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, e := range r.incoming[cur] {
			if members[e.Source] && !seen[e.Source] {
				seen[e.Source] = true
				stack = append(stack, e.Source)
			}
		}
	}
	return seen
}
