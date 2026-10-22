package workflows

import (
	"errors"
	"fmt"
	"slices"
	"strings"

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
// fires on the first edge taken and cancels the branches that lost. A First of
// that waits for a success also changes what a failure means upstream of it:
// a failed step in one of its branches drops that branch from the race
// instead of failing the run.
//
// A Loop is the one cycle allowed. It takes its body outcome; once every step
// in the body has finished, the loop runs again, checks its until condition,
// and either resets the body for another pass or takes its done outcome. The
// edges from the body back into the loop are drawn but never followed.
//
// A For each that runs steps starts its body once per item, each in a scope
// of its own (see scope.go), so the path itself skips over its body.
//
// The main path can also stop part way to continue as new (see handoff.go):
// it returns errHandoff, and the next run calls runPath again, picking up from
// the routing state in r.mainPath.
func (r *flowRun) runPath(ctx workflow.Context, entry flow.Node, entryOutput any) error {
	members := r.def.Reachable(entry.ID)
	for id := range r.inEach {
		delete(members, id)
	}
	return r.runRegion(ctx, r.top, region{
		entry: entry, handle: flow.HandleOut, output: entryOutput, members: members,
		lane: entry.ID, main: entry.ID == r.main.ID,
	})
}

// region is the part of the flow one runRegion call runs: a path from its
// entry, or a For each body for one item.
type region struct {
	// entry is where the region starts: a path's entry step, whose output
	// is output, or the For each whose body it is. handle is the entry's
	// outcome that leads in.
	entry   flow.Node
	handle  string
	output  any
	members map[string]bool
	// lane is the path's entry step (an item's is its For each's).
	lane string
	main bool
}

// runRegion runs the steps of a region in a scope, as runPath describes.
func (r *flowRun) runRegion(ctx workflow.Context, sc *scope, reg region) error {
	entry, members, isMain := reg.entry, reg.members, reg.main
	now := workflow.Now(ctx)

	ps := r.mainPath
	if !isMain || ps == nil {
		ps = &pathResume{Edges: map[string]edgeState{}, TakenOrder: map[string]int{}, Looping: map[string]bool{}, RaceFailures: map[string][]string{}}
	}
	resuming := isMain && r.mainPath != nil
	if isMain {
		r.mainPath = ps
	}
	// The path's routing state lives in ps, so the main path's can be carried
	// to the next run as it stands.
	if ps.Looping == nil {
		ps.Looping = map[string]bool{}
	}
	if ps.RaceFailures == nil {
		ps.RaceFailures = map[string][]string{}
	}
	edges, takenOrder, looping, raceFailures := ps.Edges, ps.TakenOrder, ps.Looping, ps.RaceFailures

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
	if !resuming {
		// A lane runs again for every message, so its steps start fresh.
		for _, id := range r.order {
			if members[id] && id != entry.ID {
				sc.set(id, func(ns *flow.NodeState) { *ns = flow.NodeState{Status: flow.NodePending} })
			}
		}
		if sc.item == nil {
			sc.set(entry.ID, func(ns *flow.NodeState) {
				ns.Status, ns.StartedAt, ns.EndedAt, ns.Handle = flow.NodeCompleted, &now, &now, reg.handle
				ns.Invocations++
			})
			sc.complete(entry, reg.handle, reg.output)
		}
		decide(entry.ID, reg.handle)
	}

	// decideLoop routes a loop's outcome. Taking the body leaves done and
	// limit undecided, because the loop will decide again after the pass.
	// Finishing leaves the body's edges as the last pass left them.
	decideLoop := func(id, handle string) {
		for _, e := range r.outgoing[id] {
			switch {
			case e.Handle() == handle:
				edges[e.ID] = taken
				takenOrder[e.ID] = len(takenOrder) + 1
			case handle == flow.HandleBody, e.Handle() == flow.HandleBody:
			default:
				edges[e.ID] = notTaken
			}
		}
	}

	results := workflow.NewBufferedChannel(ctx, len(members))
	pathCtx, cancelPath := workflow.WithCancel(ctx)
	defer cancelPath()
	cancels := map[string]workflow.CancelFunc{}
	lostTo := map[string]string{} // step -> label of the branch that won the race
	running := 0
	var failure error

	// raceOf maps each step in a branch of a "first to succeed" race to that
	// race. Steps every branch shares (before the fan-out) aren't in a
	// branch: if one of those fails, there is nothing left to race. With
	// nested races, a step belongs to the innermost one, which comes first in
	// topological order.
	raceOf := map[string]string{}
	for _, id := range r.order {
		node := r.nodes[id]
		if !members[id] || node.Type != flow.TypeFirstOf {
			continue
		}
		if c, _ := flow.DecodeConfig[flow.FirstOfConfig](node); c.Wins != flow.FirstOfSucceeds {
			continue
		}
		inBranches := map[string]int{}
		for _, e := range r.incoming[id] {
			branch := r.ancestors(e.Source, members)
			branch[e.Source] = true
			for step := range branch {
				inBranches[step]++
			}
		}
		for step, n := range inBranches {
			if _, nested := raceOf[step]; n < len(r.incoming[id]) && !nested {
				raceOf[step] = id
			}
		}
	}

	// fail records a failed step. In a branch of a "first to succeed" race
	// that hasn't been decided, it drops the branch: the rest of the branch
	// is skipped and the race goes on. Anywhere else it fails the path and
	// cancels every step still running.
	fail := func(id string, err error) {
		end := workflow.Now(ctx)
		sc.set(id, func(ns *flow.NodeState) {
			if ns.StartedAt == nil {
				ns.StartedAt = &end
			}
			ns.Status, ns.EndedAt, ns.Waiting, ns.Error = flow.NodeFailed, &end, nil, describeError(err)
		})
		if race, ok := raceOf[id]; ok && sc.node(race).Status == flow.NodePending {
			raceFailures[race] = append(raceFailures[race], r.nodes[id].Label+": "+describeError(err))
			for _, e := range r.outgoing[id] {
				edges[e.ID] = notTaken
			}
			return
		}
		if failure == nil {
			failure = err
			cancelPath()
		}
	}

	skip := func(id, reason string) {
		end := workflow.Now(ctx)
		// A For each body is never part of the path; it is skipped with
		// its For each.
		for _, step := range append([]string{id}, sortedKeys(r.eachBody[id])...) {
			sc.set(step, func(ns *flow.NodeState) {
				ns.Status, ns.Waiting, ns.Error = flow.NodeSkipped, nil, reason
				if ns.StartedAt != nil {
					ns.EndedAt = &end
				}
			})
		}
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
			switch sc.node(id).Status {
			case flow.NodeRunning, flow.NodeWaiting:
				if cancel, ok := cancels[id]; ok {
					lostTo[id] = winnerLabel
					cancel()
				} else {
					// A loop between passes has nothing running to cancel.
					delete(looping, id)
					skip(id, "lost the race to "+winnerLabel)
				}
			case flow.NodePending:
				skip(id, "lost the race to "+winnerLabel)
			}
		}
		at := workflow.Now(ctx)
		sc.set(node.ID, func(ns *flow.NodeState) { ns.Status, ns.StartedAt, ns.EndedAt = flow.NodeCompleted, &at, &at })
		winnerKey := r.nodes[winner.Source].Key
		sc.complete(node, flow.HandleOut, map[string]any{"winner": winnerKey, "output": sc.step(winnerKey)})
		decide(node.ID, flow.HandleOut)
	}

	// allFailed ends a "first to succeed" race in which no branch succeeded.
	// It takes the step's error outcome when one is connected, and fails like
	// any other step when not.
	allFailed := func(node flow.Node) {
		msg := "every branch failed: " + strings.Join(raceFailures[node.ID], "; ")
		for _, e := range r.outgoing[node.ID] {
			if e.Handle() == flow.HandleError {
				at := workflow.Now(ctx)
				sc.set(node.ID, func(ns *flow.NodeState) { ns.Status, ns.StartedAt, ns.EndedAt = flow.NodeCompleted, &at, &at })
				sc.complete(node, flow.HandleError, map[string]any{"error": msg, "failures": raceFailures[node.ID]})
				decide(node.ID, flow.HandleError)
				return
			}
		}
		fail(node.ID, temporal.NewNonRetryableApplicationError(msg, "AllBranchesFailed", nil))
	}

	// stopping is when the main path starts nothing new: it is handing off
	// to the next run, or an End run step has ended the run.
	stopping := func() bool { return isMain && (r.handoff || r.ending) }
	interrupted := map[string]bool{} // steps stopped for the hand-off or the end

	launchReady := func() {
		if stopping() {
			return
		}
		for changed := true; changed; {
			changed = false
			for _, id := range r.order {
				if failure != nil {
					return
				}
				if !members[id] || sc.node(id).Status != flow.NodePending {
					continue
				}
				node := r.nodes[id]
				decided, anyTaken := true, false
				for _, e := range r.incoming[id] {
					if r.back[e.ID] {
						continue // a loop's own back edge never gates it
					}
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
					if node.Type == flow.TypeFirstOf && len(raceFailures[id]) > 0 {
						allFailed(node)
					} else {
						skip(id, "")
					}
					changed = true
					continue
				}

				// A saga member whose failure is handled routes it, like a step
				// with an error outcome.
				role, inSaga := r.sagaOf[id]
				routesErrors := inSaga && !role.Unwinds
				for _, e := range r.outgoing[id] {
					routesErrors = routesErrors || e.Handle() == flow.HandleError
				}
				env := sc.env()
				start := workflow.Now(ctx)
				sc.set(id, func(ns *flow.NodeState) { ns.Status, ns.StartedAt = flow.NodeRunning, &start })
				stepCtx, cancel := workflow.WithCancel(pathCtx)
				cancels[id] = cancel
				running++
				workflow.Go(stepCtx, func(ctx workflow.Context) {
					handle, output, err := r.runNode(ctx, sc, node, env, routesErrors, reg.lane)
					results.Send(ctx, stepResult{NodeID: node.ID, Handle: handle, Output: output, Err: err})
				})
			}
			// A loop whose pass has finished runs again. Inner loops go
			// first: an outer pass isn't over while an inner loop still
			// has passes to run.
			for _, id := range r.order {
				if looping[id] && r.passFinished(id, looping) {
					delete(looping, id)
					sc.set(id, func(ns *flow.NodeState) { ns.Status = flow.NodePending })
					changed = true
				}
			}
		}
	}

	// When the main path stops, steps that are only waiting stop too; their
	// waits resume in the next run (handoff) or are over (End run). Steps
	// doing work finish first.
	if isMain {
		workflow.Go(pathCtx, func(ctx workflow.Context) {
			for {
				stoppable := func() bool {
					if !stopping() {
						return false
					}
					for id := range cancels {
						st := sc.node(id).Status
						if !interrupted[id] && (st == flow.NodeWaiting || r.ending) {
							return true
						}
					}
					return false
				}
				if err := workflow.Await(ctx, stoppable); err != nil {
					return
				}
				for _, id := range r.order {
					if cancel, ok := cancels[id]; ok && !interrupted[id] && (sc.node(id).Status == flow.NodeWaiting || r.ending) {
						interrupted[id] = true
						cancel()
					}
				}
			}
		})
	}

	launchReady()
	for running > 0 {
		var res stepResult
		results.Receive(ctx, &res)
		running--
		delete(cancels, res.NodeID)
		node := r.nodes[res.NodeID]
		end := workflow.Now(ctx)

		if res.Err != nil && stopping() && (errors.Is(res.Err, errStepHandoff) || (interrupted[node.ID] && temporal.IsCanceledError(res.Err))) {
			delete(interrupted, node.ID)
			if r.ending {
				sc.takeResume(node.ID)
				skip(node.ID, "the run was ended")
				continue
			}
			// Back to pending: the next run starts it again, and it picks
			// up from its stepResume.
			sc.set(node.ID, func(ns *flow.NodeState) { ns.Status, ns.Progress = flow.NodePending, "continues in the next run" })
			continue
		}
		sc.takeResume(node.ID)
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
			// An unwinding saga member's failure stops the path and unwinds
			// its saga (see FlowWorkflow).
			if role, ok := r.sagaOf[node.ID]; ok && role.Unwinds && !temporal.IsCanceledError(res.Err) {
				res.Err = &sagaFailure{Saga: role.Saga, Step: node.ID, Err: res.Err}
			}
			fail(node.ID, res.Err)
			if failure == nil {
				launchReady()
			}
			continue
		}

		// A handled saga member that failed with no error outcome of its own
		// carries on along its main outcome; its output says what went wrong.
		handledFailure := ""
		if role, ok := r.sagaOf[node.ID]; ok && !role.Unwinds && res.Handle == flow.HandleError && !r.hasOutcome(node.ID, flow.HandleError) {
			res.Handle = flow.Handles[node.Type][0]
			if out, ok := res.Output.(map[string]any); ok {
				handledFailure, _ = out["error"].(string)
			}
		}

		if node.Type == flow.TypeLoop {
			pass := sc.node(node.ID).Invocations
			if res.Handle == flow.HandleBody && pass > 1 {
				// Another pass: the body's steps and edges start fresh.
				for id := range r.loopBody[node.ID] {
					sc.set(id, func(ns *flow.NodeState) { *ns = flow.NodeState{Status: flow.NodePending} })
					delete(raceFailures, id)
					for _, e := range r.outgoing[id] {
						delete(edges, e.ID)
					}
				}
			}
			sc.complete(node, res.Handle, res.Output)
			if res.Handle == flow.HandleBody {
				looping[node.ID] = true
				sc.set(node.ID, func(ns *flow.NodeState) { ns.Waiting, ns.Progress = nil, fmt.Sprintf("pass %d", pass) })
			} else {
				sc.set(node.ID, func(ns *flow.NodeState) {
					ns.Status, ns.EndedAt, ns.Waiting, ns.Progress = flow.NodeCompleted, &end, nil, fmt.Sprintf("%d passes", pass-1)
				})
			}
			decideLoop(node.ID, res.Handle)
			if failure == nil {
				launchReady()
			}
			continue
		}

		sc.set(node.ID, func(ns *flow.NodeState) {
			ns.Status, ns.EndedAt, ns.Waiting, ns.Handle = flow.NodeCompleted, &end, nil, res.Handle
			if handledFailure != "" {
				ns.Error, ns.Progress = handledFailure, "failed; its saga carries on"
			}
		})
		sc.complete(node, res.Handle, res.Output)
		// A saga member that completed registers its compensation step.
		if role, ok := r.sagaOf[node.ID]; ok && role.Compensation != "" && handledFailure == "" && sc.item == nil {
			r.register(sagaEntry{Member: node.ID, Step: role.Compensation})
		}
		decide(node.ID, res.Handle)
		if failure == nil {
			launchReady()
		}
	}

	if failure != nil || (isMain && r.ending) {
		reason := "not reached"
		if failure == nil {
			reason = "the run was ended"
		}
		for _, id := range r.order {
			switch {
			case looping[id]:
				delete(looping, id)
				skip(id, reason)
			case members[id] && sc.node(id).Status == flow.NodePending:
				skip(id, reason)
			}
		}
		return failure
	}
	if isMain && r.handoff {
		for id := range members {
			if looping[id] || sc.node(id).Status == flow.NodePending {
				return errHandoff // work left: the next run carries on
			}
		}
	}
	return nil
}

// sortedKeys returns a set's members in a deterministic order.
func sortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// passFinished reports whether a loop's body has finished its pass: every
// step in it is done (completed, skipped, or failed), and no loop nested in
// it still has passes to run.
func (r *flowRun) passFinished(loopID string, looping map[string]bool) bool {
	for id := range r.loopBody[loopID] {
		if looping[id] {
			return false
		}
		switch r.state.Nodes[id].Status {
		case flow.NodeCompleted, flow.NodeSkipped, flow.NodeFailed:
		default:
			return false
		}
	}
	return true
}

// ancestors returns the steps upstream of id within one path.
func (r *flowRun) ancestors(id string, members map[string]bool) map[string]bool {
	seen := map[string]bool{}
	stack := []string{id}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, e := range r.incoming[cur] {
			if members[e.Source] && !seen[e.Source] && !r.back[e.ID] {
				seen[e.Source] = true
				stack = append(stack, e.Source)
			}
		}
	}
	return seen
}
