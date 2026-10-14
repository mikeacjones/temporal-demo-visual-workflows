package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"

	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/flow"
	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/workflows"
)

// Starting a run, and the two ways to start one with a message attached:
// Signal-With-Start and Update-With-Start. Both need a Workflow ID the sender
// can name without knowing whether the run exists, so they are for keyed
// flows: the key is part of the ID.

// keyOf works out a keyed flow's key from its start input.
func keyOf(def flow.Definition, input any) (string, error) {
	v, err := flow.Eval(def.Settings.KeyedBy, flow.Env(input, nil))
	key := flow.Stringify(v)
	if err != nil || key == "" {
		return "", fmt.Errorf("couldn't work out the key from %s", def.Settings.KeyedBy)
	}
	return key, nil
}

// startOptions are the options every start of the flow uses. A keyed run's
// Workflow ID is its key's, and the flow's ID policies say what happens when
// that ID is taken.
func (s *Server) startOptions(u user, def flow.Definition, in flow.RunInput) client.StartWorkflowOptions {
	opts := client.StartWorkflowOptions{
		ID:        s.flowRunPrefix(u.Key, def.ID) + "run-" + randomSuffix(),
		TaskQueue: s.cfg.TaskQueue,
		// Fairness keyed by user: every user shares one task queue, and this
		// keeps one user's burst (a big For each) from starving everyone
		// else's runs. Priority orders work within that.
		Priority: temporal.Priority{PriorityKey: def.Settings.Priority, FairnessKey: u.Key},
		Memo:     runMemo(in),
	}
	if in.Key != "" {
		opts.ID = s.keyedRunID(u.Key, def.ID, in.Key)
		opts.WorkflowIDConflictPolicy = conflictPolicy(def.Settings.ConflictPolicy())
		opts.WorkflowIDReusePolicy = reusePolicy(def.Settings.ReusePolicy())
		// A start the policies refuse is an error, not the old run's handle,
		// so the caller learns it was refused.
		opts.WorkflowExecutionErrorWhenAlreadyStarted = true
	}
	return opts
}

// startErrorStatus is the HTTP status for a start that failed before a run
// existed: 409 when the Workflow ID policies refused it, 400 otherwise.
func startErrorStatus(err error) int {
	var taken *serviceerror.WorkflowExecutionAlreadyStarted
	if errors.As(err, &taken) {
		return http.StatusConflict
	}
	return http.StatusBadRequest
}

func conflictPolicy(name string) enumspb.WorkflowIdConflictPolicy {
	switch name {
	case flow.ConflictFail:
		return enumspb.WORKFLOW_ID_CONFLICT_POLICY_FAIL
	case flow.ConflictTerminateExisting:
		return enumspb.WORKFLOW_ID_CONFLICT_POLICY_TERMINATE_EXISTING
	default:
		return enumspb.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING
	}
}

func reusePolicy(name string) enumspb.WorkflowIdReusePolicy {
	switch name {
	case flow.ReuseAllowDuplicateFailedOnly:
		return enumspb.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE_FAILED_ONLY
	case flow.ReuseRejectDuplicate:
		return enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE
	default:
		return enumspb.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE
	}
}

// keyedInput builds the run input for a message sent with start: the
// message's payload is also the start input, as it would be for a plain
// start, and key names the run.
func (s *Server) keyedInput(ctx context.Context, r *http.Request, u user, def flow.Definition, key string, payload any, source string) (flow.RunInput, error) {
	if def.Settings.KeyedBy == "" {
		return flow.RunInput{}, fmt.Errorf("%s isn't keyed: starting with a message needs one run per key (set Keyed by in flow settings)", def.Name)
	}
	in, err := s.runInput(ctx, r, u, def, payload, source)
	if err != nil {
		return in, err
	}
	if key == "" {
		if key, err = keyOf(def, in.Input); err != nil {
			return in, err
		}
	}
	in.Key = key
	return in, nil
}

// signalWithStart delivers a Signal to the key's run, starting the run first
// if it isn't running. Atomic: the run and the Signal are created together.
func (s *Server) signalWithStart(ctx context.Context, r *http.Request, u user, def flow.Definition, key, name string, payload any, source string) (string, error) {
	if !slices.Contains(def.SignalNames(), name) {
		return "", fmt.Errorf("%s doesn't handle a signal called %q", def.Name, name)
	}
	in, err := s.keyedInput(ctx, r, u, def, key, payload, source)
	if err != nil {
		return "", err
	}
	opts := s.startOptions(u, def, in)
	// Delivering to the running run is the point of Signal-With-Start, so it
	// can't fail on one: "fail" behaves as "use existing" here.
	if opts.WorkflowIDConflictPolicy == enumspb.WORKFLOW_ID_CONFLICT_POLICY_FAIL {
		opts.WorkflowIDConflictPolicy = enumspb.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING
	}
	run, err := s.tc.SignalWithStartWorkflow(ctx, opts.ID, name, payload, opts, workflows.FlowWorkflow, in)
	if err != nil {
		return "", err
	}
	s.log.Info("signal with start", "workflow_id", run.GetID(), "flow", def.ID, "signal", name, "source", source)
	return run.GetID(), nil
}

// updateWithStart sends an Update to the key's run, starting the run first if
// the conflict policy allows, and waits for the On update lane's response.
// Unlike Signal-With-Start it isn't atomic: if the Update can't be delivered
// (no worker answers in time), the run may still have started.
func (s *Server) updateWithStart(ctx context.Context, r *http.Request, u user, def flow.Definition, key, name string, payload any, source string) (started, error) {
	if !slices.Contains(def.UpdateNames(), name) {
		return started{}, fmt.Errorf("%s doesn't handle an update called %q", def.Name, name)
	}
	in, err := s.keyedInput(ctx, r, u, def, key, payload, source)
	if err != nil {
		return started{}, err
	}
	opts := s.startOptions(u, def, in)
	res := started{WorkflowID: opts.ID}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	handle, err := s.tc.UpdateWithStartWorkflow(ctx, client.UpdateWithStartWorkflowOptions{
		StartWorkflowOperation: s.tc.NewWithStartWorkflowOperation(opts, workflows.FlowWorkflow, in),
		UpdateOptions: client.UpdateWorkflowOptions{
			UpdateName: name, Args: []any{payload}, WaitForStage: client.WorkflowUpdateStageCompleted,
		},
	})
	if err == nil {
		err = handle.Get(ctx, &res.Response)
	}
	if err != nil {
		return res, err
	}
	res.Responded = true
	s.log.Info("update with start", "workflow_id", opts.ID, "flow", def.ID, "update", name, "source", source)
	return res, nil
}
