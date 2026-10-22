package workflows

import (
	"fmt"
	"strings"

	"go.temporal.io/sdk/workflow"

	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/flow"
)

// Sagas (see flow/saga.go): each saga keeps a stack of compensations, pushed
// as its members complete, like defers in Go. An unwinding member's failure
// runs the stack newest first: a built-in rollback is an Activity, and a
// compensation step runs as a short path of its own, entered from the Saga
// step. Then the Saga takes its compensated outcome, or failed when a
// compensation failed.

// sagaEntry is one registered compensation.
type sagaEntry struct {
	Member string
	// Step is the compensation chain's first step; Call is a built-in
	// rollback instead.
	Step    string               `json:",omitempty"`
	Call    *activityCall        `json:",omitempty"`
	Options flow.ActivityOptions `json:",omitempty"`
}

// sagaFailure is an unwinding member's failure: the main path stops, and its
// saga unwinds.
type sagaFailure struct {
	Saga, Step string
	Err        error
}

func (e *sagaFailure) Error() string { return e.Err.Error() }
func (e *sagaFailure) Unwrap() error { return e.Err }

// undoHandle is the internal outcome by which a Saga step enters one
// member's compensation chain.
func undoHandle(member string) string { return "undo:" + member }

// wireSagas indexes every member's role, and joins each Saga step to its
// compensation chains with internal edges (not part of the flow), so a chain
// runs as a path entered from its Saga.
func (r *flowRun) wireSagas() {
	r.sagaOf = r.def.SagaRoles()
	r.sagaStacks = map[string][]sagaEntry{}
	for member, role := range r.sagaOf {
		if role.Compensation == "" {
			continue
		}
		e := flow.Edge{ID: "undo:" + member, Source: role.Saga, Target: role.Compensation, SourceHandle: undoHandle(member)}
		r.outgoing[role.Saga] = append(r.outgoing[role.Saga], e)
		r.incoming[role.Compensation] = append(r.incoming[role.Compensation], e)
	}
	for _, n := range r.def.Nodes {
		if n.Type == flow.TypeSaga {
			r.state.Sagas[n.ID] = flow.SagaState{}
		}
	}
}

// register pushes a compensation onto its saga's stack.
func (r *flowRun) register(e sagaEntry) {
	role := r.sagaOf[e.Member]
	r.sagaStacks[role.Saga] = append(r.sagaStacks[role.Saga], e)
	st := r.state.Sagas[role.Saga]
	st.Registered = append(st.Registered, e.Member)
	r.state.Sagas[role.Saga] = st
	r.dirty = true
}

// unwind runs a saga's registered compensations, newest first, then its
// outcome. It returns nil when an outcome ran and finished, so the run
// completes; otherwise the error the run fails with.
func (r *flowRun) unwind(ctx workflow.Context, sf *sagaFailure) error {
	// A disconnected context, so compensation also runs for a cancelled run.
	ctx, _ = workflow.NewDisconnectedContext(ctx)
	saga := r.nodes[sf.Saga]
	cause := describeError(sf.Err)
	r.setSaga(saga.ID, func(st *flow.SagaState) { st.Status, st.Trigger, st.Error = "unwinding", sf.Step, cause })

	info := map[string]any{"error": cause, "failedStep": r.nodes[sf.Step].Key}
	stack := r.sagaStacks[saga.ID]
	var failures []string
	for i := len(stack) - 1; i >= 0; i-- {
		e := stack[i]
		var err error
		if e.Call != nil {
			r.set(e.Member, func(ns *flow.NodeState) { ns.Undo = "running" })
			err = workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, activityOptions(e.Member+"-undo", e.Options)), e.Call.Activity, e.Call.Arg).Get(ctx, nil)
			status := "done"
			if err != nil {
				status = "failed"
			}
			r.set(e.Member, func(ns *flow.NodeState) { ns.Undo = status })
		} else {
			err = r.runRegion(ctx, r.top, region{
				entry: saga, handle: undoHandle(e.Member), output: info,
				members: r.def.Reachable(e.Step), lane: saga.ID,
			})
		}
		if err != nil {
			failures = append(failures, r.nodes[e.Member].Label+": "+describeError(err))
			workflow.GetLogger(ctx).Warn("compensation failed", "saga", saga.ID, "member", e.Member, "error", describeError(err))
		}
	}

	handle, status := flow.HandleCompensated, "compensated"
	if len(failures) > 0 {
		handle, status = flow.HandleFailed, "failed"
	}
	r.setSaga(saga.ID, func(st *flow.SagaState) { st.Status = status })
	out := map[string]any{"error": cause, "failedStep": r.nodes[sf.Step].Key, "compensated": len(stack) - len(failures)}
	if len(failures) > 0 {
		out["failures"] = failures
	}
	if r.hasOutcome(saga.ID, handle) {
		return r.runRegion(ctx, r.top, region{entry: saga, handle: handle, output: out, members: r.def.Reachable(saga.ID), lane: saga.ID})
	}
	end := workflow.Now(ctx)
	r.set(saga.ID, func(ns *flow.NodeState) {
		ns.Status, ns.Handle, ns.EndedAt = flow.NodeCompleted, handle, &end
		_, ns.Output = normalize(out)
	})
	if len(failures) > 0 {
		return fmt.Errorf("%s; compensation failed for %s", cause, strings.Join(failures, "; "))
	}
	return sf.Err
}

func (r *flowRun) setSaga(id string, change func(*flow.SagaState)) {
	st := r.state.Sagas[id]
	change(&st)
	r.state.Sagas[id] = st
	r.dirty = true
}
