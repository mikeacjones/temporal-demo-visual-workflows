package workflows

import (
	"encoding/json"
	"errors"
	"time"

	"go.temporal.io/sdk/workflow"

	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/flow"
)

// Continue-As-New, for every flow.
//
// Any run can outgrow its history: a keyed entity handling events for months,
// a loop paging through a large dataset, a For each over thousands of items.
// When Temporal suggests continuing as new, the run hands off to a fresh run
// at the next safe point, carrying everything it needs to pick up where it
// left off:
//
//  1. The main path stops starting new steps.
//  2. Steps that are only waiting (for a Signal, a condition, an approval, a
//     timer, a batch to fill, a fix) are interrupted. Waiting is pure
//     Workflow state, so the next run re-enters the same wait with the same
//     deadline.
//  3. Steps doing work (an Activity, a child Workflow) finish first: an
//     Activity can't move to another run, and children would be terminated.
//     For each stops starting items (children, or its body's steps) and
//     records how far it got; the items already running finish here.
//  4. Lanes already running finish, and Signals that have arrived are
//     recorded, so nothing in flight is lost.
//
// The decision is Temporal's alone: the run never counts its own events.

// errHandoff is what runPath returns when the main path has stopped at a safe
// point for Continue-As-New.
var errHandoff = errors.New("continuing as new")

// errStepHandoff is what a step returns when it has stopped part way to hand
// off (For each, between children). Its progress is in its stepResume.
var errStepHandoff = errors.New("step paused for continue-as-new")

// stepResume is what one interrupted step needs to carry on in the next run.
type stepResume struct {
	// Until is the deadline of a wait, or when a timer fires.
	Until *time.Time `json:"until,omitempty"`
	// Start is when an expression timer started; it counts from there.
	Start *time.Time `json:"start,omitempty"`
	// Pass is the pass a loop was pausing before.
	Pass int `json:"pass,omitempty"`
	// Fix is the request a step paused for a fix would send, and why it
	// failed.
	Fix      *flow.HTTPRequestSpec `json:"fix,omitempty"`
	FixError string                `json:"fixError,omitempty"`
	Collect  *collectState         `json:"collect,omitempty"`
	ForEach  *forEachState         `json:"forEach,omitempty"`
}

type collectState struct {
	Events     []any     `json:"events"`
	Seen       []string  `json:"seen,omitempty"`
	Duplicates int       `json:"duplicates"`
	FirstAt    time.Time `json:"firstAt"`
}

type forEachState struct {
	// Next is the first item not yet started; every item before it is done.
	Next     int      `json:"next"`
	Done     int      `json:"done"`
	Winners  []any    `json:"winners,omitempty"`
	Failures []string `json:"failures,omitempty"`
	// Results is kept only for small lists; large ones report counts.
	Results []any `json:"results,omitempty"`
	// RateAt is when the next item is due under a start rate.
	RateAt *time.Time `json:"rateAt,omitempty"`
}

// interpState is the interpreter's progress, carried in ResumeState.
type interpState struct {
	// MainDone is set once the main path has finished; Path is where it is
	// otherwise.
	MainDone  bool                              `json:"mainDone"`
	Path      *pathResume                       `json:"path,omitempty"`
	Resumes   map[string]*stepResume            `json:"resumes,omitempty"`
	Queue     map[string][]any                  `json:"queue,omitempty"`
	LaneQueue map[string][]any                  `json:"laneQueue,omitempty"`
	Undos     []undoStep                        `json:"undos,omitempty"`
	Approvals map[string]*flow.ApprovalDecision `json:"approvals,omitempty"`
	Fixes     map[string]*flow.FixDecision      `json:"fixes,omitempty"`
	Responded bool                              `json:"responded,omitempty"`
	Response  any                               `json:"response,omitempty"`
}

// pathResume is the main path's routing state.
type pathResume struct {
	Edges        map[string]edgeState `json:"edges"`
	TakenOrder   map[string]int       `json:"takenOrder"`
	Looping      map[string]bool      `json:"looping,omitempty"`
	RaceFailures map[string][]string  `json:"raceFailures,omitempty"`
}

// watchForContinueAsNew flags the run for hand-off as soon as Temporal
// suggests it. The flag is read at the run's safe points.
func (r *flowRun) watchForContinueAsNew(ctx workflow.Context) {
	if err := workflow.Await(ctx, func() bool { return workflow.GetInfo(ctx).GetContinueAsNewSuggested() }); err != nil {
		return
	}
	workflow.GetLogger(ctx).Info("Temporal suggests continuing as new; handing off at the next safe point")
	r.handoff = true
	r.dirty = true
}

// continueAsNew finishes the hand-off: it waits for running lanes and message
// handlers, records every Signal that has already arrived, and starts the
// next run with this run's state. A new worker build, if one became current,
// takes over from here.
func (r *flowRun) continueAsNew(ctx workflow.Context) error {
	// Lanes running now finish; events still queued for a lane aren't
	// started here (see serveSignalLane) but carried to the next run.
	for {
		if err := workflow.Await(ctx, func() bool { return !r.laneRunning() && workflow.AllHandlersFinished(ctx) }); err != nil {
			return err
		}
		if !r.drainSignals(ctx) {
			break
		}
	}
	opts := workflow.ContinueAsNewErrorOptions{}
	if workflow.GetInfo(ctx).GetTargetWorkerDeploymentVersionChanged() {
		opts.InitialVersioningBehavior = workflow.ContinueAsNewVersioningBehaviorAutoUpgrade
	}
	next := r.in
	next.Resume = r.resumeState()
	r.flush(ctx)
	return workflow.NewContinueAsNewErrorWithOptions(ctx, opts, FlowWorkflow, next)
}

func (r *flowRun) resumeState() *flow.ResumeState {
	st := interpState{
		MainDone: r.mainDone, Path: r.mainPath, Resumes: r.resumes,
		Queue: r.queue, LaneQueue: r.laneQueue, Undos: r.undos,
		Approvals: r.approvals, Fixes: r.fixes, Responded: r.responded, Response: r.earlyResponse,
	}
	raw, _ := json.Marshal(st)
	// Outputs live in Steps; the next run rebuilds each node's from there,
	// so they aren't carried twice.
	nodes := make(map[string]flow.NodeState, len(r.state.Nodes))
	for id, ns := range r.state.Nodes {
		ns.Output = nil
		nodes[id] = ns
	}
	return &flow.ResumeState{
		Vars: r.vars, Events: r.state.Events, Steps: r.steps, Generation: r.state.Generation + 1,
		Nodes: nodes, Interpreter: raw,
	}
}

// restore takes up a previous run's state.
func (r *flowRun) restore(res *flow.ResumeState) {
	for k, v := range res.Vars {
		r.vars[k] = v
	}
	for k, v := range res.Steps {
		r.steps[k] = v
	}
	for name, ev := range res.Events {
		r.state.Events[name] = ev
		var last any
		_ = json.Unmarshal(ev.Last, &last)
		r.latest[name] = last
	}
	r.state.Generation = res.Generation
	for id, ns := range res.Nodes {
		if ns.Status == flow.NodeCompleted {
			// A For each body's steps have no single output to rebuild.
			if node, ok := r.nodes[id]; ok && r.inEach[id] == "" {
				_, ns.Output = normalize(r.steps[node.Key])
			}
		}
		r.state.Nodes[id] = ns
	}
	if len(res.Interpreter) == 0 {
		// From a keyed run before the interpreter carried its own state:
		// its main path had finished.
		r.mainDone = true
		return
	}
	var st interpState
	_ = json.Unmarshal(res.Interpreter, &st)
	r.mainDone, r.mainPath = st.MainDone, st.Path
	r.undos, r.responded, r.earlyResponse = st.Undos, st.Responded, st.Response
	for k, v := range st.Resumes {
		r.resumes[k] = v
	}
	for k, v := range st.Queue {
		r.queue[k] = v
	}
	for k, v := range st.LaneQueue {
		r.laneQueue[k] = v
	}
	for k, v := range st.Approvals {
		r.approvals[k] = v
	}
	for k, v := range st.Fixes {
		r.fixes[k] = v
	}
}

// laneRunning reports whether any lane is part way through an invocation.
func (r *flowRun) laneRunning() bool {
	for _, busy := range r.laneBusy {
		if busy {
			return true
		}
	}
	return false
}

// takeResume returns, and forgets, what an interrupted step left for its
// next start.
func (r *flowRun) takeResume(nodeID string) *stepResume {
	res := r.resumes[nodeID]
	delete(r.resumes, nodeID)
	return res
}

// noteResume records what a step would need if it were interrupted now.
func (r *flowRun) noteResume(nodeID string, change func(*stepResume)) {
	res := r.resumes[nodeID]
	if res == nil {
		res = &stepResume{}
		r.resumes[nodeID] = res
	}
	change(res)
}
