package workflows

import (
	"fmt"
	"maps"
	"slices"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/flow"
)

// runNode executes one step and returns the outcome to follow and the step's
// output. Each case is the Temporal primitive behind that step type. lane is
// the entry step of the path the step belongs to.
func (r *flowRun) runNode(ctx workflow.Context, node flow.Node, env map[string]any, routesErrors bool, lane string) (string, any, error) {
	var a *Activities

	// When a step has an "error" edge, a failure (after Temporal has used up
	// its retries) becomes a branch of the flow instead of failing the path.
	routeError := func(err error) (string, any, error) {
		if routesErrors && !temporal.IsCanceledError(err) {
			return flow.HandleError, map[string]any{"error": describeError(err)}, nil
		}
		return "", nil, err
	}

	switch node.Type {
	case flow.TypeHTTP:
		c, err := flow.DecodeConfig[flow.HTTPConfig](node)
		if err != nil {
			return "", nil, err
		}
		opts := c.ActivityOptions().Resolve(r.defaults)
		req, err := renderRequest(flow.HTTPRequestSpec{Method: c.Method, URL: c.URL, Headers: c.Headers, Body: c.Body}, env)
		if err != nil {
			return "", nil, templateError(node, err)
		}
		req.NonRetryableStatus = opts.Retry.NonRetryableStatusCodes
		var resp HTTPResponse
		// The step ID as Activity ID lets the run view put each pending
		// attempt and its last failure on the right step.
		err = workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, activityOptions(node.ID, opts)), a.HTTPRequest, req).Get(ctx, &resp)
		if err != nil {
			return routeError(err)
		}
		if c.Undo != nil {
			// Render the undo now, while this step's output is at hand, so it
			// can refer to it: {{ steps.charge.body.transactionId }}.
			undoEnv := maps.Clone(env)
			steps := maps.Clone(env["steps"].(map[string]any))
			steps[node.Key], _ = normalize(resp)
			undoEnv["steps"] = steps
			undo, err := renderRequest(*c.Undo, undoEnv)
			if err != nil {
				return "", nil, templateError(node, err)
			}
			r.undos = append(r.undos, undoStep{NodeID: node.ID, Request: undo, Options: opts})
		}
		return flow.HandleOut, resp, nil

	case flow.TypeWaitSignal:
		c, err := flow.DecodeConfig[flow.WaitSignalConfig](node)
		if err != nil {
			return "", nil, err
		}
		w := flow.Waiting{Kind: "signal", SignalName: c.SignalName}
		arrived := func() bool { return len(r.queue[c.SignalName]) > 0 }
		if handle, timedOut, err := r.await(ctx, node.ID, w, c.TimeoutSeconds, arrived); timedOut || err != nil {
			return handle, map[string]any{"timedOut": true}, err
		}
		payload := r.queue[c.SignalName][0]
		r.queue[c.SignalName] = r.queue[c.SignalName][1:]
		ev := r.state.Events[c.SignalName]
		ev.Pending = len(r.queue[c.SignalName])
		r.state.Events[c.SignalName] = ev
		return flow.HandleReceived, map[string]any{"payload": payload}, nil

	case flow.TypeWaitCondition:
		c, err := flow.DecodeConfig[flow.WaitConditionConfig](node)
		if err != nil {
			return "", nil, err
		}
		// Re-evaluated whenever the run's state changes: an event arriving or
		// a variable being set. Until the data exists the expression may not
		// evaluate; that just means "not yet".
		met := func() bool {
			v, err := flow.Eval(c.Condition, env)
			return err == nil && flow.Truthy(v)
		}
		w := flow.Waiting{Kind: "condition", SignalName: c.SignalName, Details: c.Condition}
		if handle, timedOut, err := r.await(ctx, node.ID, w, c.TimeoutSeconds, met); timedOut || err != nil {
			return handle, map[string]any{"timedOut": true}, err
		}
		out := map[string]any{"met": true}
		if c.SignalName != "" {
			out["event"] = r.latest[c.SignalName]
		}
		return flow.HandleOut, out, nil

	case flow.TypeApproval:
		c, err := flow.DecodeConfig[flow.ApprovalConfig](node)
		if err != nil {
			return "", nil, err
		}
		w := flow.Waiting{Kind: "approval"}
		if w.Title, err = flow.RenderString(c.Title, env); err != nil {
			return "", nil, templateError(node, err)
		}
		if w.Details, err = flow.RenderString(c.Details, env); err != nil {
			return "", nil, templateError(node, err)
		}
		delete(r.approvals, node.ID) // a lane may ask again
		decided := func() bool { return r.approvals[node.ID] != nil }
		if handle, timedOut, err := r.await(ctx, node.ID, w, c.TimeoutSeconds, decided); timedOut || err != nil {
			// Nobody answered in time. That is its own outcome, not a
			// rejection: no one made a decision.
			return handle, map[string]any{"decision": "timeout"}, err
		}
		d := r.approvals[node.ID]
		handle := flow.HandleRejected
		if d.Approved {
			handle = flow.HandleApproved
		}
		return handle, map[string]any{"approved": d.Approved, "comment": d.Comment, "by": d.By, "decidedAt": workflow.Now(ctx)}, nil

	case flow.TypeDelay:
		c, err := flow.DecodeConfig[flow.DelayConfig](node)
		if err != nil {
			return "", nil, err
		}
		start := workflow.Now(ctx)
		if c.Mode != "expression" {
			d := c.DelayDuration()
			until := start.Add(d)
			r.wait(node.ID, flow.Waiting{Kind: "timer", Until: &until})
			// A durable timer: it fires on schedule even if no worker is
			// running in the meantime.
			if err := workflow.Sleep(ctx, d); err != nil {
				return "", nil, err
			}
			return flow.HandleOut, map[string]any{"waitedSeconds": d.Seconds()}, nil
		}
		// An expression timer recalculates whenever a variable changes, so a
		// lane can move the deadline while the timer is running.
		for {
			v, err := flow.Eval(c.Expression, env)
			if err != nil {
				return "", nil, templateError(node, err)
			}
			target, err := flow.TimerTarget(v, start)
			if err != nil {
				return "", nil, templateError(node, err)
			}
			remaining := target.Sub(workflow.Now(ctx))
			if remaining <= 0 {
				break
			}
			r.wait(node.ID, flow.Waiting{Kind: "timer", Until: &target, Details: c.Expression})
			seen := r.varsVersion
			changed, err := workflow.AwaitWithTimeout(ctx, remaining, func() bool { return r.varsVersion != seen })
			if err != nil {
				return "", nil, err
			}
			if !changed {
				break
			}
		}
		return flow.HandleOut, map[string]any{"waitedSeconds": workflow.Now(ctx).Sub(start).Seconds()}, nil

	case flow.TypeCondition:
		c, err := flow.DecodeConfig[flow.ConditionConfig](node)
		if err != nil {
			return "", nil, err
		}
		v, err := flow.Eval(c.Expression, env)
		if err != nil {
			return "", nil, templateError(node, err)
		}
		if flow.Truthy(v) {
			return flow.HandleTrue, map[string]any{"result": true}, nil
		}
		return flow.HandleFalse, map[string]any{"result": false}, nil

	case flow.TypeTransform:
		c, err := flow.DecodeConfig[flow.TransformConfig](node)
		if err != nil {
			return "", nil, err
		}
		out, err := flow.RenderJSON(c.Output, env)
		if err != nil {
			return "", nil, templateError(node, err)
		}
		return flow.HandleOut, out, nil

	case flow.TypeSetVariable:
		c, err := flow.DecodeConfig[flow.SetVariableConfig](node)
		if err != nil {
			return "", nil, err
		}
		set := map[string]any{}
		for _, as := range c.Assignments {
			v, err := flow.Render(as.Value, env)
			if err != nil {
				return "", nil, templateError(node, err)
			}
			value, _ := normalize(v)
			// Assigned in place: conditions and timers hold this same map.
			r.vars[as.Name] = value
			set[as.Name] = value
		}
		r.varsVersion++
		r.dirty = true
		return flow.HandleOut, map[string]any{"set": set}, nil

	case flow.TypeRespond:
		c, err := flow.DecodeConfig[flow.RespondConfig](node)
		if err != nil {
			return "", nil, err
		}
		out, err := flow.RenderJSON(c.Value, env)
		if err != nil {
			return "", nil, templateError(node, err)
		}
		r.responses[lane] = out
		return flow.HandleOut, out, nil

	case flow.TypeAgent:
		c, err := flow.DecodeConfig[flow.AgentConfig](node)
		if err != nil {
			return "", nil, err
		}
		instructions, err := flow.RenderString(c.Instructions, env)
		if err != nil {
			return "", nil, templateError(node, err)
		}
		// The agent runs as a child Workflow: its loop gets its own history,
		// its own Query and Update for the trace and human questions, and it
		// can be inspected on its own in the Temporal UI.
		childID := r.childID("agent", node, lane, "")
		r.set(node.ID, func(ns *flow.NodeState) { ns.ChildWorkflowID = childID })
		ctx = workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{WorkflowID: childID})
		var res AgentResult
		err = workflow.ExecuteChildWorkflow(ctx, AgentWorkflow, AgentInput{
			Instructions:  instructions,
			Tools:         c.Tools,
			AllowAskHuman: c.AllowAskHuman,
			MaxTurns:      c.MaxTurns,
			Context:       map[string]any{"input": env["input"], "steps": env["steps"], "vars": r.vars},
			ModelOptions:  c.ModelActivity.Resolve(agentModelDefaults),
			ToolOptions:   c.ToolActivity.Resolve(flow.ActivityOptions{Retry: flow.RetryConfig{MaxAttempts: 3}}.Resolve(r.defaults)),
		}).Get(ctx, &res)
		if err != nil {
			return routeError(err)
		}
		return flow.HandleOut, res, nil

	case flow.TypeRunFlow:
		c, err := flow.DecodeConfig[flow.RunFlowConfig](node)
		if err != nil {
			return "", nil, err
		}
		input, err := renderInput(c.Input, env)
		if err != nil {
			return "", nil, templateError(node, err)
		}
		childID := r.childID("child", node, lane, "")
		r.set(node.ID, func(ns *flow.NodeState) { ns.ChildWorkflowID = childID })
		out, err := r.runChild(ctx, c.FlowID, childID, parentClosePolicy(c.ParentClosePolicy), input)
		if err != nil {
			return routeError(err)
		}
		return flow.HandleOut, out, nil

	case flow.TypeForEach:
		c, err := flow.DecodeConfig[flow.ForEachConfig](node)
		if err != nil {
			return "", nil, err
		}
		v, err := flow.Eval(c.Items, env)
		if err != nil {
			return "", nil, templateError(node, err)
		}
		items, ok := v.([]any)
		if !ok {
			return "", nil, templateError(node, fmt.Errorf("items must be a list, got %T", v))
		}
		if len(items) > 200 {
			return "", nil, templateError(node, fmt.Errorf("%d items is more than the 200 a For each step runs", len(items)))
		}
		// Fan out with child Workflows, at most Concurrency at a time. Each
		// child has its own history, so a long list doesn't grow this run's.
		results := make([]any, len(items))
		done, active := 0, 0
		var failure error
		progress := func() {
			r.set(node.ID, func(ns *flow.NodeState) { ns.Progress = fmt.Sprintf("%d of %d done", done, len(items)) })
		}
		progress()
		childCtx, cancelChildren := workflow.WithCancel(ctx)
		defer cancelChildren()
		for i, item := range items {
			if err := workflow.Await(ctx, func() bool { return active < c.Concurrency || failure != nil }); err != nil {
				return "", nil, err
			}
			if failure != nil {
				break
			}
			itemEnv := maps.Clone(env)
			itemEnv["item"], itemEnv["index"] = item, i
			input, err := renderInput(c.Input, itemEnv)
			if err != nil {
				return "", nil, templateError(node, err)
			}
			active++
			childID := r.childID("each", node, lane, fmt.Sprint(i))
			workflow.Go(childCtx, func(ctx workflow.Context) {
				out, err := r.runChild(ctx, c.FlowID, childID, enumspb.PARENT_CLOSE_POLICY_TERMINATE, input)
				active--
				if err != nil {
					if failure == nil {
						failure = err
						cancelChildren()
					}
					return
				}
				results[i] = out
				done++
				progress()
			})
		}
		if err := workflow.Await(ctx, func() bool { return active == 0 }); err != nil {
			return "", nil, err
		}
		if failure != nil {
			return routeError(failure)
		}
		return flow.HandleOut, map[string]any{"results": results, "count": len(items)}, nil
	}
	return "", nil, temporal.NewNonRetryableApplicationError("unknown step type "+string(node.Type), "InvalidFlow", nil)
}

// await marks the step as waiting and blocks until ready() is true, with an
// optional timeout (0 waits indefinitely). Nothing runs while it waits: the
// Workflow is woken only by a Signal, an Update, or its timer.
func (r *flowRun) await(ctx workflow.Context, nodeID string, w flow.Waiting, timeoutSeconds int, ready func() bool) (handle string, timedOut bool, err error) {
	if timeoutSeconds <= 0 {
		r.wait(nodeID, w)
		return "", false, workflow.Await(ctx, ready)
	}
	timeout := time.Duration(timeoutSeconds) * time.Second
	until := workflow.Now(ctx).Add(timeout)
	w.Until = &until
	r.wait(nodeID, w)
	ok, err := workflow.AwaitWithTimeout(ctx, timeout, ready)
	if err != nil {
		return "", false, err
	}
	if !ok {
		return flow.HandleTimeout, true, nil
	}
	return "", false, nil
}

// runChild runs a saved flow as a child Workflow and returns its step
// outputs by key. The child's definition is a snapshot taken when this run
// started, like the parent's own.
func (r *flowRun) runChild(ctx workflow.Context, flowID, childID string, policy enumspb.ParentClosePolicy, input any) (map[string]any, error) {
	sub, ok := r.in.Subflows[flowID]
	if !ok {
		return nil, temporal.NewNonRetryableApplicationError(fmt.Sprintf("flow %q was not found when this run started", flowID), "FlowNotFound", nil)
	}
	ctx = workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		WorkflowID: childID, ParentClosePolicy: policy,
		// The same memo the API sets on runs it starts, so child runs list
		// under their own flow.
		Memo: map[string]any{"flowId": sub.ID, "flowName": sub.Name, "flowVersion": sub.Version, "source": "parent"},
	})
	var child flow.RunState
	err := workflow.ExecuteChildWorkflow(ctx, FlowWorkflow, flow.RunInput{
		Flow: sub, Input: input, Source: "parent", CallbackBase: r.in.CallbackBase, Subflows: r.in.Subflows,
	}).Get(ctx, &child)
	if err != nil {
		return nil, err
	}
	outputs := map[string]any{"workflowId": childID}
	for _, n := range child.Flow.Nodes {
		if ns := child.Nodes[n.ID]; ns.Status == flow.NodeCompleted && len(ns.Output) > 0 {
			outputs[n.Key], _ = normalize(ns.Output)
		}
	}
	return outputs, nil
}

func renderRequest(spec flow.HTTPRequestSpec, env map[string]any) (HTTPRequest, error) {
	req := HTTPRequest{Method: spec.Method, Headers: map[string]string{}}
	var err error
	if req.URL, err = flow.RenderString(spec.URL, env); err != nil {
		return req, err
	}
	if req.Body, err = flow.RenderString(spec.Body, env); err != nil {
		return req, err
	}
	for _, k := range slices.Sorted(maps.Keys(spec.Headers)) {
		if req.Headers[k], err = flow.RenderString(spec.Headers[k], env); err != nil {
			return req, err
		}
	}
	return req, nil
}

func renderInput(template string, env map[string]any) (any, error) {
	if template == "" {
		return nil, nil
	}
	return flow.RenderJSON(template, env)
}

func parentClosePolicy(name string) enumspb.ParentClosePolicy {
	switch name {
	case "abandon":
		return enumspb.PARENT_CLOSE_POLICY_ABANDON
	case "request_cancel":
		return enumspb.PARENT_CLOSE_POLICY_REQUEST_CANCEL
	default:
		return enumspb.PARENT_CLOSE_POLICY_TERMINATE
	}
}
