// Package workflows holds the Temporal Workflows and Activities that run
// flows drawn in the builder.
package workflows

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/flow"
)

// FlowWorkflow runs one flow. It is a small interpreter: the definition
// arrives as input, and each step becomes the matching Temporal primitive (an
// Activity, a Signal, an Update, a timer, a child Workflow). Because the
// definition is part of the input it is fixed in history and replays exactly,
// even if the flow is edited while this run is in flight.
//
// A flow has one main path, from its trigger, and optional lanes that start
// at an On signal or On update step. Lanes are how a running Workflow reacts
// to messages: each Signal runs its lane, each accepted Update runs its lane
// and returns the Respond step's value. Everything shares one deterministic
// event loop, so lanes never interleave mid-step.
//
// The run finishes when the main path does, after any lane already running
// finishes, or when an End run step runs. A keyed flow is an entity: after its
// main path it keeps serving events for its key until an End run step (or, as
// a session, until it has been idle for its idle timeout). Any run continues
// as new when Temporal suggests it (see handoff.go).
func FlowWorkflow(ctx workflow.Context, in flow.RunInput) (flow.RunState, error) {
	r, err := newFlowRun(ctx, in)
	if err != nil {
		return flow.RunState{}, temporal.NewNonRetryableApplicationError(err.Error(), "InvalidFlow", nil)
	}
	if err := r.registerHandlers(ctx); err != nil {
		return r.state, err
	}
	workflow.Go(ctx, r.publishProgress)
	workflow.Go(ctx, r.watchForContinueAsNew)

	var failure error
	if !r.mainDone {
		failure = r.runPath(ctx, r.main, in.Input)
		if errors.Is(failure, errHandoff) {
			return r.state, r.continueAsNew(ctx)
		}
		r.mainDone, r.mainErr, r.mainPath = true, failure, nil
	}
	if failure == nil && in.Key != "" && !r.ending {
		if err := r.serveKey(ctx); err != nil {
			return r.state, err // Continue-As-New, or cancellation
		}
	}

	// Let lanes that are already running (and queued events) finish, then
	// stop accepting new ones.
	r.closing = true
	_ = workflow.Await(ctx, func() bool { return r.lanesIdle() && workflow.AllHandlersFinished(ctx) })

	end := workflow.Now(ctx)
	r.state.EndedAt = &end
	if failure != nil {
		r.state.Status, r.state.Error = flow.RunFailed, describeError(failure)
		r.compensate(ctx)
		r.flush(ctx)
		return r.state, failure
	}
	r.state.Status = flow.RunCompleted
	r.flush(ctx)
	return r.state, nil
}

type flowRun struct {
	def      flow.Definition
	in       flow.RunInput
	runID    string
	started  time.Time
	nodes    map[string]flow.Node
	order    []string
	incoming map[string][]flow.Edge
	outgoing map[string][]flow.Edge
	main     flow.Node
	// back are the edges that close loops; loopBody holds each loop's
	// steps, and loopOf the innermost loop each step repeats in.
	back     map[string]bool
	loopBody map[string]map[string]bool
	loopOf   map[string]string
	// defaults are the flow's Activity settings, already resolved against
	// the system defaults.
	defaults flow.ActivityOptions

	state     flow.RunState
	steps     map[string]any
	vars      map[string]any
	latest    map[string]any   // newest payload per Signal name: events.<name>
	queue     map[string][]any // events not yet consumed by Wait for signal
	approvals map[string]*flow.ApprovalDecision
	fixes     map[string]*flow.FixDecision
	// handoff is set when Temporal suggests continuing as new; mainPath is
	// the main path's routing state, and resumes what interrupted steps need,
	// both carried to the next run.
	handoff  bool
	mainPath *pathResume
	resumes  map[string]*stepResume
	// ending is set by an End run step.
	ending bool
	// earlyResponse is the main path's Respond value, returned to whoever
	// started the run through the early_response Update.
	earlyResponse any
	responded     bool
	mainDone      bool
	mainErr       error
	// varsVersion changes whenever a variable is set, waking expression
	// timers so they can recalculate.
	varsVersion int
	eventCount  int

	signals    map[string]workflow.ReceiveChannel
	signalLane map[string]string // Signal name -> the On signal lane it runs
	laneQueue  map[string][]any  // per On signal lane: events waiting to run it
	laneBusy   map[string]bool
	responses  map[string]any // per On update lane: the Respond step's value
	closing    bool

	undos []undoStep
	dirty bool
}

type undoStep struct {
	NodeID  string
	Request HTTPRequest
	Options flow.ActivityOptions
}

func newFlowRun(ctx workflow.Context, in flow.RunInput) (*flowRun, error) {
	def := in.Flow
	order, err := def.TopologicalOrder()
	if err != nil {
		return nil, err
	}
	info := workflow.GetInfo(ctx)
	r := &flowRun{
		def: def, in: in, order: order,
		runID:      info.WorkflowExecution.ID,
		started:    workflow.Now(ctx),
		nodes:      map[string]flow.Node{},
		incoming:   map[string][]flow.Edge{},
		outgoing:   map[string][]flow.Edge{},
		defaults:   def.Settings.Activity.Resolve(flow.SystemActivityDefaults),
		steps:      map[string]any{},
		vars:       map[string]any{},
		latest:     map[string]any{},
		queue:      map[string][]any{},
		approvals:  map[string]*flow.ApprovalDecision{},
		fixes:      map[string]*flow.FixDecision{},
		resumes:    map[string]*stepResume{},
		signals:    map[string]workflow.ReceiveChannel{},
		signalLane: map[string]string{},
		laneQueue:  map[string][]any{},
		laneBusy:   map[string]bool{},
		responses:  map[string]any{},
		back:       def.BackEdges(),
		loopBody:   map[string]map[string]bool{},
		loopOf:     map[string]string{},
	}
	for _, n := range def.Nodes {
		r.nodes[n.ID] = n
		if n.Type == flow.TypeTrigger {
			r.main = n
		}
	}
	for _, e := range def.Edges {
		r.incoming[e.Target] = append(r.incoming[e.Target], e)
		r.outgoing[e.Source] = append(r.outgoing[e.Source], e)
	}
	// Outer loops come first in order, so an inner loop overwrites loopOf
	// for its own steps.
	for _, id := range order {
		if r.nodes[id].Type != flow.TypeLoop {
			continue
		}
		r.loopBody[id] = def.LoopBody(id)
		for _, step := range order {
			if r.loopBody[id][step] {
				r.loopOf[step] = id
			}
		}
	}
	for _, v := range def.Settings.Variables {
		var value any
		if v.Initial != "" {
			_ = json.Unmarshal([]byte(v.Initial), &value)
		}
		r.vars[v.Name] = value
	}

	r.state = flow.RunState{
		FlowID: def.ID, FlowName: def.Name, FlowVersion: def.Version, Source: in.Source,
		Status: flow.RunRunning, StartedAt: r.started,
		Nodes: map[string]flow.NodeState{}, Events: map[string]flow.EventState{},
		Vars: r.vars, Key: in.Key, Flow: def,
	}
	if in.CallbackBase != "" {
		r.state.CallbackURL = in.CallbackBase + "/runs/" + r.runID
	}
	for _, n := range def.Nodes {
		r.state.Nodes[n.ID] = flow.NodeState{Status: flow.NodePending}
	}
	if in.Resume != nil {
		r.restore(in.Resume)
		if len(in.Resume.Interpreter) == 0 {
			for id := range def.Reachable(r.main.ID) {
				r.state.Nodes[id] = flow.NodeState{Status: flow.NodeCompleted, Progress: "earlier run"}
			}
		}
	}
	r.dirty = true
	return r, nil
}

// registerHandlers sets up every message the run answers before it does any
// work, so nothing sent early is missed.
func (r *flowRun) registerHandlers(ctx workflow.Context) error {
	if err := workflow.SetQueryHandler(ctx, flow.QueryState, func() (flow.RunState, error) {
		return r.state, nil
	}); err != nil {
		return err
	}
	// Visible variables can be read from outside without touching history.
	if err := workflow.SetQueryHandler(ctx, flow.QueryVars, func() (map[string]any, error) {
		visible := map[string]any{}
		for _, v := range r.def.Settings.Variables {
			if v.Visible {
				visible[v.Name] = r.vars[v.Name]
			}
		}
		return visible, nil
	}); err != nil {
		return err
	}

	// Approvals are Updates rather than Signals: the validator rejects a
	// decision for a step that isn't waiting (or was already decided), and the
	// caller learns that immediately instead of the decision being dropped.
	if err := workflow.SetUpdateHandlerWithOptions(ctx, flow.UpdateApprove,
		func(ctx workflow.Context, d flow.ApprovalDecision) error {
			r.approvals[d.NodeID] = &d
			r.dirty = true
			return nil
		},
		workflow.UpdateHandlerOptions{Validator: func(d flow.ApprovalDecision) error {
			ns := r.state.Nodes[d.NodeID]
			if ns.Status != flow.NodeWaiting || ns.Waiting == nil || ns.Waiting.Kind != "approval" || r.approvals[d.NodeID] != nil {
				return fmt.Errorf("step %q is not waiting for an approval", d.NodeID)
			}
			return nil
		}},
	); err != nil {
		return err
	}

	// A step paused after its retries ran out resumes through this Update.
	if err := workflow.SetUpdateHandlerWithOptions(ctx, flow.UpdateFix,
		func(ctx workflow.Context, d flow.FixDecision) error {
			r.fixes[d.NodeID] = &d
			r.dirty = true
			return nil
		},
		workflow.UpdateHandlerOptions{Validator: func(d flow.FixDecision) error {
			ns := r.state.Nodes[d.NodeID]
			if ns.Status != flow.NodeWaiting || ns.Waiting == nil || ns.Waiting.Kind != "fix" || r.fixes[d.NodeID] != nil {
				return fmt.Errorf("step %q is not waiting for a fix", d.NodeID)
			}
			if d.Action != "retry" && d.Action != "fail" {
				return fmt.Errorf("action must be retry or fail")
			}
			return nil
		}},
	); err != nil {
		return err
	}

	// Early return: whoever starts the run sends this Update with the start
	// (Update-With-Start) and gets the main path's Respond value as soon as
	// the run reaches it, while the run carries on.
	if r.def.RespondsEarly() {
		if err := workflow.SetUpdateHandler(ctx, flow.UpdateEarlyResponse, func(ctx workflow.Context) (any, error) {
			if err := workflow.Await(ctx, func() bool { return r.responded || r.mainDone || r.handoff || r.ending }); err != nil {
				return nil, err
			}
			if !r.responded {
				reason := "it took a path without a Respond step"
				switch {
				case r.mainErr != nil:
					reason = "it failed: " + describeError(r.mainErr)
				case r.handoff:
					reason = "it continued as new first"
				case r.ending:
					reason = "an End run step ended it"
				}
				return nil, temporal.NewApplicationError("the run finished without responding: "+reason, "NoResponse")
			}
			return r.earlyResponse, nil
		}); err != nil {
			return err
		}
	}

	signalLanes := map[string]flow.Node{}
	for _, n := range r.def.Nodes {
		switch n.Type {
		case flow.TypeOnSignal:
			c, _ := flow.DecodeConfig[flow.OnSignalConfig](n)
			signalLanes[c.SignalName] = n
			workflow.Go(ctx, r.serveSignalLane(n))
		case flow.TypeOnUpdate:
			if err := r.registerUpdateLane(ctx, n); err != nil {
				return err
			}
		}
	}

	// One listener per Signal name records every event as it arrives, so a
	// step can react to an event that came before it started. POSTs to the
	// run's callback URL arrive as these Signals; so do Signals sent from the
	// Temporal UI or CLI.
	for _, name := range r.def.SignalNames() {
		ch := workflow.GetSignalChannel(ctx, name)
		r.signals[name] = ch
		lane, hasLane := signalLanes[name]
		if hasLane {
			r.signalLane[name] = lane.ID
		}
		workflow.Go(ctx, func(ctx workflow.Context) {
			for {
				var payload any
				ch.Receive(ctx, &payload)
				r.record(ctx, name, payload)
			}
		})
	}
	return nil
}

// record stores one event: as the latest value for conditions, in the queue
// for Wait for signal, and in its lane's queue if a lane handles it.
func (r *flowRun) record(ctx workflow.Context, name string, payload any) {
	value, raw := normalize(payload)
	now := workflow.Now(ctx)
	r.latest[name] = value
	r.queue[name] = append(r.queue[name], value)
	ev := r.state.Events[name]
	ev.Count++
	ev.Last, ev.LastAt, ev.Pending = raw, &now, len(r.queue[name])
	r.state.Events[name] = ev
	r.eventCount++
	if lane, ok := r.signalLane[name]; ok && !r.closing {
		r.laneQueue[lane] = append(r.laneQueue[lane], value)
	}
	r.dirty = true
}

// drainSignals records Signals that have arrived but not yet been received.
// Before continuing as new, every Signal in this run's history must be
// handled here, or it would be lost: the next run starts with an empty
// buffer.
func (r *flowRun) drainSignals(ctx workflow.Context) bool {
	drained := false
	for _, name := range r.def.SignalNames() {
		for {
			var payload any
			if !r.signals[name].ReceiveAsync(&payload) {
				break
			}
			r.record(ctx, name, payload)
			drained = true
		}
	}
	return drained
}

// serveSignalLane runs an On signal lane once per event, in arrival order. A
// failed step marks itself failed; the run carries on.
func (r *flowRun) serveSignalLane(lane flow.Node) func(workflow.Context) {
	return func(ctx workflow.Context) {
		for {
			// While handing off, queued events wait for the next run.
			if err := workflow.Await(ctx, func() bool { return (len(r.laneQueue[lane.ID]) > 0 && !r.handoff) || r.closing }); err != nil {
				return
			}
			if len(r.laneQueue[lane.ID]) == 0 {
				return // closing, and nothing left to handle
			}
			payload := r.laneQueue[lane.ID][0]
			r.laneQueue[lane.ID] = r.laneQueue[lane.ID][1:]
			r.laneBusy[lane.ID] = true
			if err := r.runPath(ctx, lane, payload); err != nil {
				workflow.GetLogger(ctx).Warn("lane failed; the run continues", "lane", lane.Label, "error", describeError(err))
			}
			r.laneBusy[lane.ID] = false
		}
	}
}

// registerUpdateLane makes an On update lane callable. The validator runs
// before anything is written to history: a rejected Update leaves no trace,
// and the caller gets the reason at once. An accepted one runs the lane and
// returns its Respond step's value to the caller.
func (r *flowRun) registerUpdateLane(ctx workflow.Context, lane flow.Node) error {
	c, err := flow.DecodeConfig[flow.OnUpdateConfig](lane)
	if err != nil {
		return err
	}
	return workflow.SetUpdateHandlerWithOptions(ctx, c.UpdateName,
		func(ctx workflow.Context, request any) (any, error) {
			// One invocation of a lane at a time; a second Update waits its turn.
			if err := workflow.Await(ctx, func() bool { return !r.laneBusy[lane.ID] }); err != nil {
				return nil, err
			}
			r.laneBusy[lane.ID] = true
			defer func() { r.laneBusy[lane.ID] = false }()
			delete(r.responses, lane.ID)
			if err := r.runPath(ctx, lane, request); err != nil {
				return nil, temporal.NewApplicationError(describeError(err), "LaneFailed")
			}
			return r.responses[lane.ID], nil
		},
		workflow.UpdateHandlerOptions{Validator: func(request any) error {
			if r.closing || r.ending {
				return fmt.Errorf("this run is finishing and no longer accepts %s", c.UpdateName)
			}
			if c.Validator == "" {
				return nil
			}
			env := r.env()
			env["request"] = request
			v, err := flow.Eval(c.Validator, env)
			if err != nil || !flow.Truthy(v) {
				return fmt.Errorf("rejected: %s is not true for this request", c.Validator)
			}
			return nil
		}},
	)
}

func (r *flowRun) lanesIdle() bool {
	for _, q := range r.laneQueue {
		if len(q) > 0 {
			return false
		}
	}
	for _, busy := range r.laneBusy {
		if busy {
			return false
		}
	}
	return true
}

// serveKey keeps a keyed run serving its key's events after the main path.
// An entity (no idle timeout) lives until an End run step, however long it
// waits: waiting costs nothing and adds no history, and the run continues as
// new whenever Temporal suggests it. A session ends after its idle timeout
// with no events; the key's next event then starts a fresh run.
func (r *flowRun) serveKey(ctx workflow.Context) error {
	idle := time.Duration(r.def.Settings.IdleTimeoutSeconds) * time.Second
	for {
		seen := r.eventCount
		woken := func() bool { return r.eventCount != seen || r.handoff || r.ending }
		active := true
		var err error
		if idle > 0 {
			active, err = workflow.AwaitWithTimeout(ctx, idle, woken)
		} else {
			err = workflow.Await(ctx, woken)
		}
		switch {
		case err != nil:
			return err
		case r.ending:
			return nil
		case r.handoff:
			return r.continueAsNew(ctx)
		case !active && r.lanesIdle():
			return nil // a session, idle: its run ends
		}
	}
}

// compensate runs the undo requests of completed steps, newest first. It
// uses a disconnected context so it also runs when the run was cancelled.
func (r *flowRun) compensate(ctx workflow.Context) {
	if len(r.undos) == 0 {
		return
	}
	ctx, _ = workflow.NewDisconnectedContext(ctx)
	var a *Activities
	r.state.Compensation = "running"
	r.dirty = true
	failed := false
	for i := len(r.undos) - 1; i >= 0; i-- {
		u := r.undos[i]
		r.set(u.NodeID, func(ns *flow.NodeState) { ns.Undo = "running" })
		err := workflow.ExecuteActivity(
			workflow.WithActivityOptions(ctx, activityOptions(u.NodeID+"-undo", u.Options)),
			a.HTTPRequest, u.Request,
		).Get(ctx, nil)
		status := "done"
		if err != nil {
			status, failed = "failed", true
			workflow.GetLogger(ctx).Warn("undo failed", "step", u.NodeID, "error", describeError(err))
		}
		r.set(u.NodeID, func(ns *flow.NodeState) { ns.Undo = status })
	}
	r.state.Compensation = "done"
	if failed {
		r.state.Compensation = "failed"
	}
}

// env is what expressions can see. vars and events are live maps, so a
// waiting condition or timer sees changes as they happen; steps is a copy, so
// each step sees the outputs that existed when it started.
func (r *flowRun) env() map[string]any {
	steps := make(map[string]any, len(r.steps))
	for k, v := range r.steps {
		steps[k] = v
	}
	env := flow.Env(r.in.Input, steps)
	env["vars"] = r.vars
	env["events"] = r.latest
	env["key"] = r.in.Key
	// The Workflow ID is stable across retries and replays, which makes it a
	// natural idempotency key for downstream calls.
	env["run"] = map[string]any{"id": r.runID, "callbackUrl": r.state.CallbackURL, "startedAt": r.started}
	return env
}

func (r *flowRun) set(nodeID string, change func(*flow.NodeState)) {
	ns := r.state.Nodes[nodeID]
	change(&ns)
	r.state.Nodes[nodeID] = ns
	r.dirty = true
}

func (r *flowRun) wait(nodeID string, w flow.Waiting) {
	r.set(nodeID, func(ns *flow.NodeState) { ns.Status, ns.Waiting = flow.NodeWaiting, &w })
}

// publishProgress keeps a compact copy of the run's state in its memo. The
// API reads it with Describe, which needs no worker, so watching a run never
// wakes one. Changes made in the same Workflow Task are written once.
func (r *flowRun) publishProgress(ctx workflow.Context) {
	for {
		if err := workflow.Await(ctx, func() bool { return r.dirty }); err != nil {
			return
		}
		r.dirty = false
		if err := workflow.UpsertMemo(ctx, map[string]any{flow.MemoProgress: r.progress()}); err != nil {
			workflow.GetLogger(ctx).Warn("could not record progress", "error", err)
		}
	}
}

// flush writes final progress synchronously, before the run closes.
func (r *flowRun) flush(ctx workflow.Context) {
	ctx, _ = workflow.NewDisconnectedContext(ctx)
	r.dirty = false
	_ = workflow.UpsertMemo(ctx, map[string]any{flow.MemoProgress: r.progress()})
}

// Memo is limited to 40 KB on Temporal Cloud; progress leaves outputs out and
// trims what could grow.
const (
	maxProgressVarsBytes  = 8 << 10
	maxProgressEventBytes = 1 << 10
)

func (r *flowRun) progress() flow.Progress {
	p := flow.Progress{
		Status: r.state.Status, Error: truncate(r.state.Error, 300),
		Nodes: map[string]flow.NodeProgress{}, Events: map[string]flow.EventState{},
		Compensation: r.state.Compensation, Generation: r.state.Generation,
	}
	for id, ns := range r.state.Nodes {
		np := flow.NodeProgress{
			Status: ns.Status, Handle: ns.Handle, Error: truncate(ns.Error, 300),
			StartedAt: ns.StartedAt, EndedAt: ns.EndedAt, Invocations: ns.Invocations,
			Undo: ns.Undo, Progress: ns.Progress, Child: ns.ChildWorkflowID,
		}
		if w := ns.Waiting; w != nil {
			short := *w
			short.Details = truncate(short.Details, 300)
			np.Waiting = &short
		}
		p.Nodes[id] = np
	}
	// Each event's time and count, and its latest payload when it is small,
	// so event panels need no Query.
	for name, ev := range r.state.Events {
		if len(ev.Last) > maxProgressEventBytes {
			ev.Last = nil
		}
		p.Events[name] = ev
	}
	if raw, err := json.Marshal(r.vars); err == nil && len(raw) <= maxProgressVarsBytes {
		p.Vars = r.vars
	}
	return p
}

// normalize sends a value through JSON, so expressions see the same shape the
// run view does (body.total, not a Go struct field).
func normalize(v any) (any, json.RawMessage) {
	raw, _ := json.Marshal(v)
	var value any
	_ = json.Unmarshal(raw, &value)
	return value, raw
}

func templateError(node flow.Node, err error) error {
	return temporal.NewNonRetryableApplicationError(fmt.Sprintf("%s: %v", node.Label, err), "TemplateError", nil)
}

// describeError returns the innermost message, which is the one a person
// wants to read ("POST /refunds returned 503") rather than the SDK's wrapper.
func describeError(err error) string {
	var appErr *temporal.ApplicationError
	if errors.As(err, &appErr) {
		return appErr.Error()
	}
	var timeoutErr *temporal.TimeoutError
	if errors.As(err, &timeoutErr) {
		return "timed out (" + timeoutErr.TimeoutType().String() + ")"
	}
	if temporal.IsCanceledError(err) {
		return "canceled"
	}
	return err.Error()
}

// childID names a child Workflow so it is unique per lane invocation and per
// keyed generation.
func (r *flowRun) childID(kind string, node flow.Node, lane string, suffix string) string {
	id := r.runID + "-" + kind + "-" + node.Key
	if g := r.state.Generation; g > 0 {
		id += fmt.Sprintf("-g%d", g)
	}
	if n := r.state.Nodes[lane].Invocations; lane != r.main.ID && n > 0 {
		id += fmt.Sprintf("-i%d", n)
	}
	// A step in a loop starts a new child on every pass.
	if loop, ok := r.loopOf[node.ID]; ok {
		id += fmt.Sprintf("-p%d", r.state.Nodes[loop].Invocations)
	}
	if suffix != "" {
		id += "-" + suffix
	}
	return id
}
