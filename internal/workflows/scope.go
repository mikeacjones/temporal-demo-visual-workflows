package workflows

import (
	"encoding/json"
	"fmt"
	"maps"
	"strings"
	"time"

	"go.temporal.io/sdk/workflow"

	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/flow"
)

// scope is where the steps of one path keep their state. Most paths run in
// the run's own scope: step states are the run's, and outputs are
// steps.<key> for everything after. A For each that runs steps gives each
// item a scope of its own, so two items can be at different steps of the
// same body at once, each with its own outputs.
type scope struct {
	r *flowRun
	// The rest is set only for an item.
	item  *itemRef
	nodes map[string]flow.NodeState
	steps map[string]any
	// base is what the For each saw when it started; an item's steps see
	// it, plus the item and its own outputs.
	base map[string]any
	each *eachRun
}

// itemRef is the item a scope runs.
type itemRef struct {
	Each  string
	Index int
	Value any
}

func (sc *scope) node(id string) flow.NodeState {
	if sc.item == nil {
		return sc.r.state.Nodes[id]
	}
	return sc.nodes[id]
}

func (sc *scope) set(id string, change func(*flow.NodeState)) {
	if sc.item == nil {
		sc.r.set(id, change)
		return
	}
	before := sc.nodes[id]
	after := before
	change(&after)
	kept := after
	if len(kept.Output) > maxItemOutputBytes {
		// Too big to keep per item; the item's steps still read it, from
		// sc.steps.
		kept.Output = nil
	}
	sc.nodes[id] = kept
	sc.each.changed(id, before, after)
}

func (sc *scope) wait(id string, w flow.Waiting) {
	sc.set(id, func(ns *flow.NodeState) { ns.Status, ns.Waiting = flow.NodeWaiting, &w })
}

// complete records a step's output where later steps in the scope can read
// it.
func (sc *scope) complete(node flow.Node, handle string, output any) {
	value, raw := normalize(output)
	if sc.item == nil {
		sc.r.steps[node.Key] = value
	} else {
		sc.steps[node.Key] = value
	}
	sc.set(node.ID, func(ns *flow.NodeState) { ns.Handle, ns.Output = handle, raw })
}

// step reads a step's output: the item's own first, then the run's.
func (sc *scope) step(key string) any {
	if sc.item != nil {
		if v, ok := sc.steps[key]; ok {
			return v
		}
		return sc.base["steps"].(map[string]any)[key]
	}
	return sc.r.steps[key]
}

func (sc *scope) env() map[string]any {
	if sc.item == nil {
		return sc.r.env()
	}
	env := maps.Clone(sc.base)
	steps := maps.Clone(env["steps"].(map[string]any))
	maps.Copy(steps, sc.steps)
	env["steps"] = steps
	env["item"], env["index"] = sc.item.Value, sc.item.Index
	return env
}

// takeResume and noteResume keep what an interrupted step needs in the next
// run. Items are never interrupted (like children, they finish before a
// hand-off), so an item keeps nothing, and two items at the same step can't
// see each other's.
func (sc *scope) takeResume(id string) *stepResume {
	if sc.item != nil {
		return nil
	}
	return sc.r.takeResume(id)
}

func (sc *scope) noteResume(id string, change func(*stepResume)) {
	if sc.item == nil {
		sc.r.noteResume(id, change)
	}
}

// activityID is the step's ID, which lets the run view put pending
// attempts on the right step; an item's Activities add its index.
func (sc *scope) activityID(nodeID string) string {
	if sc.item == nil {
		return nodeID
	}
	return fmt.Sprintf("%s%s%d", nodeID, flow.ItemSeparator, sc.item.Index)
}

// eachRun is one run of a For each body over its items. The canvas has one
// card per step, so a body step shows a summary across items ("12 done · 3
// running"); each item's own step states are kept, for the most recent
// items, in RunState.Items.
type eachRun struct {
	r      *flowRun
	node   flow.Node
	body   map[string]bool
	counts map[string]map[flow.NodeStatus]int
	items  []*flow.ItemState
}

// maxKeptItems is how many items' states a For each keeps for the run view:
// every item running, then the most recent to finish, failures last to go.
const maxKeptItems = 50

// maxItemOutputBytes keeps a large step output out of an item's state; it
// is still in the For each's results.
const maxItemOutputBytes = 4 << 10

func (r *flowRun) newEachRun(node flow.Node) *eachRun {
	e := &eachRun{r: r, node: node, body: r.eachBody[node.ID], counts: map[string]map[flow.NodeStatus]int{}}
	for id := range e.body {
		e.counts[id] = map[flow.NodeStatus]int{}
		r.set(id, func(ns *flow.NodeState) { *ns = flow.NodeState{Status: flow.NodePending} })
	}
	r.state.Items[node.ID] = nil
	return e
}

// start opens an item's scope.
func (e *eachRun) start(env map[string]any, index int, value any, now time.Time) *scope {
	item := &flow.ItemState{Index: index, Status: flow.NodeRunning, StartedAt: &now, Nodes: map[string]flow.NodeState{}}
	if raw, err := json.Marshal(value); err == nil && len(raw) <= maxItemOutputBytes {
		item.Item = raw
	}
	e.items = append(e.items, item)
	e.publish()
	return &scope{
		r: e.r, item: &itemRef{Each: e.node.ID, Index: index, Value: value},
		nodes: item.Nodes, steps: map[string]any{}, base: env, each: e,
	}
}

// finish records how an item ended.
func (e *eachRun) finish(sc *scope, err error, now time.Time) {
	for _, item := range e.items {
		if item.Index != sc.item.Index {
			continue
		}
		item.EndedAt = &now
		item.Status = flow.NodeCompleted
		if err != nil {
			item.Status, item.Error = flow.NodeFailed, describeError(err)
		}
	}
	e.trim()
	e.publish()
}

// trim keeps every running item and the most recent finished ones, dropping
// the oldest successes before the oldest failures.
func (e *eachRun) trim() {
	for len(e.items) > maxKeptItems {
		drop := -1
		for i, item := range e.items {
			if item.Status == flow.NodeCompleted {
				drop = i
				break
			}
		}
		if drop < 0 {
			for i, item := range e.items {
				if item.Status == flow.NodeFailed {
					drop = i
					break
				}
			}
		}
		if drop < 0 {
			return
		}
		e.items = append(e.items[:drop], e.items[drop+1:]...)
	}
}

func (e *eachRun) publish() {
	e.r.state.Items[e.node.ID] = e.items
	e.r.dirty = true
}

// changed keeps a body step's summary current as an item's copy of it
// changes.
func (e *eachRun) changed(id string, before, after flow.NodeState) {
	c := e.counts[id]
	if c == nil {
		return
	}
	if before.Status != "" {
		c[before.Status]--
	}
	c[after.Status]++
	e.r.set(id, func(ns *flow.NodeState) {
		if ns.StartedAt == nil && after.StartedAt != nil {
			ns.StartedAt = after.StartedAt
		}
		switch {
		case c[flow.NodeRunning] > 0:
			ns.Status, ns.Waiting = flow.NodeRunning, nil
		case c[flow.NodeWaiting] > 0:
			ns.Status, ns.Waiting = flow.NodeWaiting, after.Waiting
		}
		if after.Status == flow.NodeCompleted && before.Status != flow.NodeCompleted {
			ns.Output, ns.Handle = after.Output, after.Handle
		}
		ns.Progress = e.summary(c)
	})
}

func (e *eachRun) summary(c map[flow.NodeStatus]int) string {
	var parts []string
	for _, s := range []struct {
		status flow.NodeStatus
		label  string
	}{{flow.NodeCompleted, "done"}, {flow.NodeRunning, "running"}, {flow.NodeWaiting, "waiting"}, {flow.NodeFailed, "failed"}, {flow.NodeSkipped, "skipped"}} {
		if n := c[s.status]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, s.label))
		}
	}
	return strings.Join(parts, " · ")
}

// close settles every body step once no item is running: done if any item
// completed it, failed if it only ever failed, skipped if no item reached it.
func (e *eachRun) close(now time.Time) {
	for id := range e.body {
		c := e.counts[id]
		e.r.set(id, func(ns *flow.NodeState) {
			ns.Waiting = nil
			switch {
			case c[flow.NodeCompleted] > 0:
				ns.Status = flow.NodeCompleted
			case c[flow.NodeFailed] > 0:
				ns.Status = flow.NodeFailed
			default:
				ns.Status, ns.Progress = flow.NodeSkipped, "no item reached it"
			}
			if ns.StartedAt != nil {
				ns.EndedAt = &now
			}
		})
	}
}

// runItem runs a For each body for one item, in its own scope, and returns
// the item's step outputs by key: the same shape a child flow's are.
func (r *flowRun) runItem(ctx workflow.Context, e *eachRun, env map[string]any, lane string, index int, value any) (map[string]any, error) {
	sc := e.start(env, index, value, workflow.Now(ctx))
	err := r.runRegion(ctx, sc, region{entry: e.node, handle: flow.HandleBody, members: e.body, lane: lane})
	e.finish(sc, err, workflow.Now(ctx))
	if err != nil {
		return nil, err
	}
	return sc.steps, nil
}
