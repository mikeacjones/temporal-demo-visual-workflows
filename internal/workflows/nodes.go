package workflows

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/flow"
)

// runNode executes one step and returns the outcome to follow and the step's
// output. Each case is the Temporal primitive behind that step type. lane is
// the entry step of the path the step belongs to.
// runNode runs one step in a scope: the run's, or one For each item's.
func (r *flowRun) runNode(ctx workflow.Context, sc *scope, node flow.Node, env map[string]any, routesErrors bool, lane string) (string, any, error) {
	// When a step has an "error" edge, a failure (after Temporal has used up
	// its retries) becomes a branch of the flow instead of failing the path.
	routeError := func(err error) (string, any, error) {
		if routesErrors && !temporal.IsCanceledError(err) {
			return flow.HandleError, map[string]any{"error": describeError(err)}, nil
		}
		return "", nil, err
	}
	// A step interrupted to continue as new left what it needs to carry on;
	// a fresh start finds nothing here.
	res := sc.takeResume(node.ID)
	now := workflow.Now(ctx)

	if flow.IsConnector(node.Type) {
		out, err := r.runConnector(ctx, sc, node, env)
		if err != nil {
			return routeError(err)
		}
		return flow.HandleOut, out, nil
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
		var a *Activities
		var resp HTTPResponse
		// Paused for a fix when the last run handed off: go straight back
		// to waiting, with the request as it stood.
		pausedForFix := res != nil && res.Fix != nil
		if pausedForFix {
			req.Method, req.URL, req.Headers, req.Body = res.Fix.Method, res.Fix.URL, res.Fix.Headers, res.Fix.Body
			err = errors.New(res.FixError)
		}
		for {
			if !pausedForFix {
				err = r.callActivity(ctx, sc, node, opts, c.RateLimit, a.HTTPRequest, req, &resp)
				if err == nil {
					break
				}
				if !c.PauseForFix || temporal.IsCanceledError(err) {
					return routeError(err)
				}
			}
			// A resumable Activity: rather than failing, wait (for free)
			// until a person corrects the request or gives up on it.
			fix, waitErr := r.awaitFix(ctx, sc, node, req, err, pausedForFix)
			pausedForFix = false
			if waitErr != nil {
				return "", nil, waitErr
			}
			if fix.Action != "retry" {
				return routeError(err)
			}
			if fix.URL != "" {
				req.URL = fix.URL
			}
			if fix.Body != nil {
				req.Body = *fix.Body
			}
			for k, v := range fix.Headers {
				if req.Headers == nil {
					req.Headers = map[string]string{}
				}
				req.Headers[k] = v
			}
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
			// In a For each body each item's undo joins the run's stack too;
			// to put one item right when it fails, draw that on its error
			// outcome.
			r.undos = append(r.undos, undoStep{NodeID: node.ID, ActivityID: sc.key(node.ID), Request: undo, Options: opts})
		}
		return flow.HandleOut, resp, nil

	case flow.TypeWaitSignal:
		c, err := flow.DecodeConfig[flow.WaitSignalConfig](node)
		if err != nil {
			return "", nil, err
		}
		if c.Collect != nil {
			return r.collect(ctx, node, c, env, res)
		}
		w := flow.Waiting{Kind: "signal", SignalName: c.SignalName, Details: c.Match}
		// The first queued event for this step: any, or with a match (in a
		// For each body, the item's), the first that matches. Events that
		// don't match stay queued for whoever they are for.
		next := func() int {
			for i, ev := range r.queue[c.SignalName] {
				if c.Match == "" {
					return i
				}
				evEnv := maps.Clone(env)
				evEnv["event"] = ev
				if v, err := flow.Eval(c.Match, evEnv); err == nil && flow.Truthy(v) {
					return i
				}
			}
			return -1
		}
		arrived := func() bool { return next() >= 0 }
		if handle, timedOut, err := r.await(ctx, sc, node.ID, w, deadline(res, c.TimeoutSeconds, now), arrived); timedOut || err != nil {
			return handle, map[string]any{"timedOut": true}, err
		}
		i := next()
		payload := r.queue[c.SignalName][i]
		r.queue[c.SignalName] = slices.Delete(slices.Clone(r.queue[c.SignalName]), i, i+1)
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
		if handle, timedOut, err := r.await(ctx, sc, node.ID, w, deadline(res, c.TimeoutSeconds, now), met); timedOut || err != nil {
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
		key := sc.key(node.ID) // in a For each body, each item asks on its own
		if res == nil {
			delete(r.approvals, key) // a lane may ask again
		}
		decided := func() bool { return r.approvals[key] != nil }
		if handle, timedOut, err := r.await(ctx, sc, node.ID, w, deadline(res, c.TimeoutSeconds, now), decided); timedOut || err != nil {
			// Nobody answered in time. That is its own outcome, not a
			// rejection: no one made a decision.
			return handle, map[string]any{"decision": "timeout"}, err
		}
		d := r.approvals[key]
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
		start := now
		if res != nil && res.Start != nil {
			start = *res.Start // the timer carries on from where it began
		}
		sc.noteResume(node.ID, func(s *stepResume) { s.Start = &start })
		if c.Mode != "expression" {
			d := c.DelayDuration()
			until := start.Add(d)
			sc.wait(node.ID, flow.Waiting{Kind: "timer", Until: &until})
			// A durable timer: it fires on schedule even if no worker is
			// running in the meantime.
			if remaining := until.Sub(now); remaining > 0 {
				if err := workflow.Sleep(ctx, remaining); err != nil {
					return "", nil, err
				}
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
			sc.wait(node.ID, flow.Waiting{Kind: "timer", Until: &target, Details: c.Expression})
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
		if lane == r.main.ID {
			// Early return: the first Respond on the main path answers
			// whoever started the run; the run carries on.
			if !r.responded {
				r.earlyResponse, r.responded = out, true
			}
			return flow.HandleOut, out, nil
		}
		r.responses[lane] = out
		return flow.HandleOut, out, nil

	case flow.TypeLoop:
		c, err := flow.DecodeConfig[flow.LoopConfig](node)
		if err != nil {
			return "", nil, err
		}
		pass := r.state.Nodes[node.ID].Invocations + 1
		resumedPause := res != nil && res.Pass > 0 && res.Until != nil
		if resumedPause {
			pass = res.Pass // it had already decided to go again; finish the pause
		}
		r.set(node.ID, func(ns *flow.NodeState) { ns.Invocations = pass })
		if pass > 1 && !resumedPause {
			// After each pass: stop when the condition holds. Until the
			// data it reads exists, it just isn't true yet.
			if v, err := flow.Eval(c.Until, env); err == nil && flow.Truthy(v) {
				return flow.HandleDone, map[string]any{"passes": pass - 1}, nil
			}
			if c.MaxPasses > 0 && pass > c.MaxPasses {
				msg := fmt.Sprintf("%s stopped after %d passes; %s never became true", node.Label, pass-1, c.Until)
				if r.hasOutcome(node.ID, flow.HandleLimit) {
					return flow.HandleLimit, map[string]any{"passes": pass - 1, "error": msg}, nil
				}
				return "", nil, temporal.NewNonRetryableApplicationError(msg, "LoopLimit", nil)
			}
		}
		if pass > 1 && c.Pause != nil {
			// A durable timer between passes: polling costs nothing while it
			// waits, and survives the worker going away.
			var until time.Time
			if resumedPause {
				until = *res.Until
			} else {
				d, err := pauseLength(*c.Pause, env, now)
				if err != nil {
					return "", nil, templateError(node, err)
				}
				until = now.Add(d)
			}
			r.noteResume(node.ID, func(s *stepResume) { s.Pass, s.Until = pass, &until })
			r.wait(node.ID, flow.Waiting{Kind: "timer", Until: &until, Details: fmt.Sprintf("before pass %d", pass)})
			if remaining := until.Sub(now); remaining > 0 {
				if err := workflow.Sleep(ctx, remaining); err != nil {
					return "", nil, err
				}
			}
			r.set(node.ID, func(ns *flow.NodeState) { ns.Status, ns.Waiting = flow.NodeRunning, nil })
		}
		return flow.HandleBody, map[string]any{"pass": pass}, nil

	case flow.TypeEnd:
		// The run completes once lanes already running finish. Steps still
		// running on the main path stop, and an entity's run ends here.
		r.ending = true
		r.dirty = true
		return flow.HandleOut, map[string]any{"ended": true}, nil

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
		childID := r.childID("agent", node, lane, sc.childSuffix())
		sc.set(node.ID, func(ns *flow.NodeState) { ns.ChildWorkflowID = childID })
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
		childID := r.childID("child", node, lane, sc.childSuffix())
		sc.set(node.ID, func(ns *flow.NodeState) { ns.ChildWorkflowID = childID })
		if c.Wait == flow.RunFlowWaitStart {
			// Fire and forget: once the child has started it is on its own
			// (it is abandoned, so this run closing doesn't stop it).
			future, err := r.startChild(ctx, c.FlowID, childID, parentClosePolicy(c.ParentClosePolicy), input)
			if err != nil {
				return routeError(err)
			}
			var exec workflow.Execution
			if err := future.GetChildWorkflowExecution().Get(ctx, &exec); err != nil {
				return routeError(err)
			}
			return flow.HandleOut, map[string]any{"workflowId": exec.ID, "runId": exec.RunID}, nil
		}
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
		if len(items) > flow.MaxForEachItems {
			return "", nil, templateError(node, fmt.Errorf("%d items is more than the %d a For each step runs; page through them with a Loop", len(items), flow.MaxForEachItems))
		}
		// How many children must succeed. Waiting for all of them fails on
		// the first failure. Waiting for fewer (the first, or a quorum)
		// tolerates failures until too few children are left to get there.
		need, tolerant := len(items), false
		switch c.FinishWhen {
		case flow.ForEachFirst:
			need, tolerant = 1, true
		case flow.ForEachCount:
			need, tolerant = c.Count, true
		}
		if need > len(items) {
			return "", nil, templateError(node, fmt.Errorf("%d must succeed, but there are only %d items", need, len(items)))
		}

		// Fan out, at most Concurrency at a time: with child Workflows, or
		// with the body's steps run inside this run, one scope per item. A
		// child has its own history but still adds a few events here; an
		// inline item adds all of its steps' events here. Either way a long
		// list can reach Continue-As-New part way: then it stops starting
		// items, lets the running ones finish, and the next run carries on
		// from the next item. Results are kept for short lists only; a long
		// list reports counts.
		inline := c.Run == flow.ForEachRunSteps
		var each *eachRun
		if inline {
			each = r.newEachRun(node)
		}
		keepResults := len(items) <= maxKeptResults
		st := forEachState{}
		if res != nil && res.ForEach != nil {
			st = *res.ForEach
		}
		results := make([]any, len(items))
		copy(results, st.Results)
		winners, failures, done := st.Winners, st.Failures, st.Done // winners in the order they finished
		active := 0
		var failure error
		handingOff := func() bool { return r.handoff && lane == r.main.ID }
		finished := func() bool { return failure != nil || (tolerant && len(winners) >= need) }
		progress := func() {
			text := fmt.Sprintf("%d of %d done", done, len(items))
			if tolerant {
				text = fmt.Sprintf("%d of %d needed", len(winners), need)
				if len(failures) > 0 {
					text += fmt.Sprintf(" · %d failed", len(failures))
				}
			}
			r.set(node.ID, func(ns *flow.NodeState) { ns.Progress = text })
		}
		progress()
		childCtx, cancelChildren := workflow.WithCancel(ctx)
		defer cancelChildren()
		next := st.Next
		// The start rate is a generic cell rate algorithm: rateAt is when
		// the next start is due if starts were evenly spaced, and a start
		// may come up to the burst's worth of intervals early. Waits are
		// durable timers.
		rateAt := st.RateAt
		var interval, early time.Duration
		if c.Rate != nil {
			interval = c.Rate.Interval()
			early = time.Duration(c.Rate.Burst()-1) * interval
		}
		for i := st.Next; i < len(items); i++ {
			item := items[i]
			if err := workflow.Await(ctx, func() bool { return active < c.Concurrency || finished() || handingOff() }); err != nil {
				return "", nil, err
			}
			if c.Rate != nil && rateAt != nil {
				if wait := rateAt.Add(-early).Sub(workflow.Now(ctx)); wait > 0 && !finished() && !handingOff() {
					if _, err := workflow.AwaitWithTimeout(ctx, wait, func() bool { return finished() || handingOff() }); err != nil {
						return "", nil, err
					}
				}
			}
			if finished() || handingOff() {
				break
			}
			next = i + 1
			if c.Rate != nil {
				due := workflow.Now(ctx)
				if rateAt != nil && rateAt.After(due) {
					due = *rateAt
				}
				due = due.Add(interval)
				rateAt = &due
			}
			active++
			var run func(ctx workflow.Context) (map[string]any, error)
			if inline {
				run = func(ctx workflow.Context) (map[string]any, error) {
					return r.runItem(ctx, each, env, lane, i, item)
				}
			} else {
				itemEnv := maps.Clone(env)
				itemEnv["item"], itemEnv["index"] = item, i
				input, err := renderInput(c.Input, itemEnv)
				if err != nil {
					return "", nil, templateError(node, err)
				}
				childID := r.childID("each", node, lane, fmt.Sprint(i))
				run = func(ctx workflow.Context) (map[string]any, error) {
					return r.runChild(ctx, c.FlowID, childID, enumspb.PARENT_CLOSE_POLICY_TERMINATE, input)
				}
			}
			workflow.Go(childCtx, func(ctx workflow.Context) {
				out, err := run(ctx)
				active--
				switch {
				case finished():
					// Decided already: this child was cancelled, or finished
					// too late to count.
				case err != nil && !tolerant:
					failure = err
					cancelChildren()
				case err != nil:
					failures = append(failures, fmt.Sprintf("item %d: %s", i, describeError(err)))
					if len(items)-len(failures) < need {
						failure = temporal.NewNonRetryableApplicationError(
							fmt.Sprintf("%d of the %d needed succeeded: %s", len(winners), need, strings.Join(failures, "; ")),
							"NotEnoughSucceeded", nil)
						cancelChildren()
					}
				default:
					if keepResults {
						results[i] = out
					}
					done++
					winners = append(winners, map[string]any{"index": i, "item": item, "output": out})
					if tolerant && len(winners) >= need {
						// Enough have succeeded: cancel the children still
						// running. Each one stops at its next step.
						cancelChildren()
					}
				}
				progress()
			})
		}
		if err := workflow.Await(ctx, func() bool { return active == 0 }); err != nil {
			return "", nil, err
		}
		if each != nil {
			each.close(workflow.Now(ctx))
		}
		if failure != nil {
			return routeError(failure)
		}
		if !finished() && next < len(items) {
			// Handing off: every item before next is done.
			st = forEachState{Next: next, Done: done, Winners: winners, Failures: failures, RateAt: rateAt}
			if keepResults {
				st.Results = results[:next]
			}
			sc.noteResume(node.ID, func(s *stepResume) { s.ForEach = &st })
			r.set(node.ID, func(ns *flow.NodeState) {
				ns.Progress = fmt.Sprintf("%d of %d done · continues in the next run", done, len(items))
			})
			return "", nil, errStepHandoff
		}
		if !tolerant {
			out := map[string]any{"count": len(items), "succeeded": done}
			if keepResults {
				out["results"] = results
			}
			return flow.HandleOut, out, nil
		}
		return flow.HandleOut, map[string]any{"winner": winners[0], "winners": winners, "count": len(items), "failed": len(failures)}, nil
	}
	return "", nil, temporal.NewNonRetryableApplicationError("unknown step type "+string(node.Type), "InvalidFlow", nil)
}

// runConnector runs a business connector step: its operation's Activity
// (SalesforceQuery, StripeCreatePayment, ...), named by the connector's spec,
// with the step's rendered parameters as its argument. If the step can be
// undone, the compensating operation joins the saga.
func (r *flowRun) runConnector(ctx workflow.Context, sc *scope, node flow.Node, env map[string]any) (map[string]any, error) {
	c, err := flow.DecodeConfig[flow.ConnectorConfig](node)
	if err != nil {
		return nil, err
	}
	conn, op, err := flow.ConnectorOperation(node.Type, c.Operation)
	if err != nil {
		return nil, temporal.NewNonRetryableApplicationError(err.Error(), "InvalidFlow", nil)
	}
	params, err := flow.RenderParams(op, c.Params, env)
	if err != nil {
		return nil, templateError(node, err)
	}
	opts := c.Activity.Resolve(r.defaults)
	var out map[string]any
	if err := r.callActivity(ctx, sc, node, opts, c.RateLimit, op.Activity, params, &out); err != nil {
		return nil, err
	}
	if c.Undo && op.Undo != nil {
		undoOp, undoParams := op.Undo(params, out)
		if undo, ok := conn.Operation(undoOp); ok {
			r.undos = append(r.undos, undoStep{NodeID: node.ID, ActivityID: sc.key(node.ID), Call: &activityCall{Activity: undo.Activity, Arg: undoParams}, Options: opts})
		}
	}
	return out, nil
}

// callActivity runs a step's Activity (fn, with arg) with the step's retry
// policy, decoding its result into result. The step ID as Activity ID lets
// the run view put each pending attempt and its last failure on the right
// step. If the step has slow retries and the quick ones are used up, it keeps
// going with the slow policy: two Activities in sequence, each with its own
// policy, so neither the Workflow nor the Activity loops.
func (r *flowRun) callActivity(ctx workflow.Context, sc *scope, node flow.Node, opts flow.ActivityOptions, limit string, fn, arg, result any) error {
	options := func(o flow.ActivityOptions) workflow.ActivityOptions {
		ao := activityOptions(sc.key(node.ID), o)
		if limit != "" {
			// A limit shared by every run: its own task queue, which Temporal
			// dispatches no faster than the limit's rate.
			ao.TaskQueue = flow.LimitTaskQueue(workflow.GetInfo(ctx).TaskQueueName, limit)
		}
		return ao
	}
	if limit != "" && !slices.Contains(r.in.SharedLimits, limit) {
		return temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("%s: rate limit %q isn't configured in this deployment (RATE_LIMITS)", node.Label, limit), "UnknownRateLimit", nil)
	}
	err := workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, options(opts)), fn, arg).Get(ctx, result)
	if err == nil || opts.SlowRetry == nil || !retriesUsedUp(err) {
		return err
	}
	sc.set(node.ID, func(ns *flow.NodeState) { ns.Progress = "quick retries used up; retrying slowly" })
	slow := opts
	slow.Retry = *opts.SlowRetry
	slow.Retry.NonRetryableErrorTypes = opts.Retry.NonRetryableErrorTypes
	// The slow phase is meant to outlast an outage, so no overall deadline.
	slow.ScheduleToCloseSeconds = 0
	err = workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, options(slow)), fn, arg).Get(ctx, result)
	if err == nil {
		sc.set(node.ID, func(ns *flow.NodeState) { ns.Progress = "succeeded on a slow retry" })
	}
	return err
}

// retriesUsedUp reports whether an Activity failed because its retry policy
// ran out (attempts or time), rather than on a non-retryable error.
func retriesUsedUp(err error) bool {
	var actErr *temporal.ActivityError
	if !errors.As(err, &actErr) {
		return false
	}
	switch actErr.RetryState() {
	case enumspb.RETRY_STATE_MAXIMUM_ATTEMPTS_REACHED, enumspb.RETRY_STATE_TIMEOUT:
		return true
	case enumspb.RETRY_STATE_NON_RETRYABLE_FAILURE, enumspb.RETRY_STATE_CANCEL_REQUESTED:
		return false
	}
	// Where the state isn't reported (the SDK's test environment), decide
	// from the cause: anything not marked non-retryable could still succeed.
	var appErr *temporal.ApplicationError
	if errors.As(err, &appErr) {
		return !appErr.NonRetryable()
	}
	return !temporal.IsCanceledError(err)
}

// awaitFix parks a step whose retries ran out until a person decides, through
// the fix_step Update, to retry it (perhaps corrected) or let it fail. The
// run view shows the request so they can edit it.
func (r *flowRun) awaitFix(ctx workflow.Context, sc *scope, node flow.Node, req HTTPRequest, cause error, resumed bool) (flow.FixDecision, error) {
	key := sc.key(node.ID)
	if !resumed {
		delete(r.fixes, key) // a decision sent before a hand-off still counts
	}
	spec := flow.HTTPRequestSpec{Method: req.Method, URL: req.URL, Headers: req.Headers, Body: req.Body}
	sc.noteResume(node.ID, func(s *stepResume) { s.Fix, s.FixError = &spec, describeError(cause) })
	sc.wait(node.ID, flow.Waiting{
		Kind: "fix", Title: "Retries ran out", Details: describeError(cause),
		Request: &flow.HTTPRequestSpec{Method: req.Method, URL: req.URL, Headers: req.Headers, Body: req.Body},
	})
	if err := workflow.Await(ctx, func() bool { return r.fixes[key] != nil }); err != nil {
		return flow.FixDecision{}, err
	}
	fix := *r.fixes[key]
	delete(r.fixes, key)
	sc.set(node.ID, func(ns *flow.NodeState) {
		ns.Status, ns.Waiting = flow.NodeRunning, nil
		if fix.Action == "retry" {
			ns.Progress = "retrying after a fix"
		}
	})
	return fix, nil
}

// collect gathers events into a batch (an event accumulator). It waits for
// the first event (up to the step's timeout), then keeps collecting until
// none arrive for the quiet period, the batch is full, or the batch's maximum
// wait has passed. Duplicates, by the dedupe key, are dropped.
func (r *flowRun) collect(ctx workflow.Context, node flow.Node, c flow.WaitSignalConfig, env map[string]any, res *stepResume) (string, any, error) {
	k := *c.Collect
	quiet := time.Duration(k.QuietSeconds) * time.Second
	// The batch so far, if it was being collected when the last run handed
	// off. The quiet period starts again from the hand-off.
	b := collectState{}
	if res != nil && res.Collect != nil {
		b = *res.Collect
	}
	seen := map[string]bool{}
	for _, key := range b.Seen {
		seen[key] = true
	}
	firstDeadline := deadline(res, c.TimeoutSeconds, workflow.Now(ctx))
	arrived := func() bool { return len(r.queue[c.SignalName]) > 0 }
	for {
		r.noteResume(node.ID, func(s *stepResume) { s.Collect, s.Until = &b, firstDeadline })
		w := flow.Waiting{Kind: "collect", SignalName: c.SignalName, Details: fmt.Sprintf("%d collected", len(b.Events))}
		var until *time.Time
		switch {
		case len(b.Events) == 0:
			until = firstDeadline
		default:
			t := workflow.Now(ctx).Add(quiet)
			if k.MaxWaitSeconds > 0 {
				if last := b.FirstAt.Add(time.Duration(k.MaxWaitSeconds) * time.Second); last.Before(t) {
					t = last
				}
			}
			until = &t
		}
		w.Until = until
		r.wait(node.ID, w)
		ok := true
		var err error
		if until != nil {
			ok, err = workflow.AwaitWithTimeout(ctx, max(until.Sub(workflow.Now(ctx)), 0), arrived)
		} else {
			err = workflow.Await(ctx, arrived)
		}
		if err != nil {
			return "", nil, err
		}
		if !ok {
			if len(b.Events) == 0 {
				return flow.HandleTimeout, map[string]any{"timedOut": true}, nil
			}
			break // quiet, or the batch's time is up
		}
		if len(b.Events) == 0 {
			b.FirstAt = workflow.Now(ctx)
		}
		for len(r.queue[c.SignalName]) > 0 {
			ev := r.queue[c.SignalName][0]
			r.queue[c.SignalName] = r.queue[c.SignalName][1:]
			if k.DedupeBy != "" {
				evEnv := maps.Clone(env)
				evEnv["event"] = ev
				v, err := flow.Eval(k.DedupeBy, evEnv)
				if key := flow.Stringify(v); err == nil && key != "" {
					if seen[key] {
						b.Duplicates++
						continue
					}
					seen[key] = true
					b.Seen = append(b.Seen, key)
				}
			}
			b.Events = append(b.Events, ev)
		}
		e := r.state.Events[c.SignalName]
		e.Pending = len(r.queue[c.SignalName])
		r.state.Events[c.SignalName] = e
		if k.MaxEvents > 0 && len(b.Events) >= k.MaxEvents {
			break
		}
	}
	return flow.HandleReceived, map[string]any{"events": b.Events, "count": len(b.Events), "duplicates": b.Duplicates}, nil
}

// maxKeptResults is the longest list whose For each output keeps every
// child's results; longer lists report counts, keeping payloads (and what
// Continue-As-New carries) small.
const maxKeptResults = 200

// pauseLength is the time a loop waits between passes.
func pauseLength(p flow.DelayConfig, env map[string]any, now time.Time) (time.Duration, error) {
	if p.Mode != "expression" {
		return p.DelayDuration(), nil
	}
	v, err := flow.Eval(p.Expression, env)
	if err != nil {
		return 0, err
	}
	target, err := flow.TimerTarget(v, now)
	if err != nil {
		return 0, err
	}
	return max(target.Sub(now), time.Second), nil
}

func (r *flowRun) hasOutcome(nodeID, handle string) bool {
	for _, e := range r.outgoing[nodeID] {
		if e.Handle() == handle {
			return true
		}
	}
	return false
}

// deadline is when a wait times out: the deadline it already had, if it is
// resuming after a hand-off, or timeoutSeconds from now. nil waits
// indefinitely.
func deadline(res *stepResume, timeoutSeconds int, now time.Time) *time.Time {
	if res != nil && res.Until != nil {
		return res.Until
	}
	if timeoutSeconds <= 0 {
		return nil
	}
	until := now.Add(time.Duration(timeoutSeconds) * time.Second)
	return &until
}

// await marks the step as waiting and blocks until ready() is true, with an
// optional timeout (0 waits indefinitely). Nothing runs while it waits: the
// Workflow is woken only by a Signal, an Update, or its timer.
func (r *flowRun) await(ctx workflow.Context, sc *scope, nodeID string, w flow.Waiting, until *time.Time, ready func() bool) (handle string, timedOut bool, err error) {
	w.Until = until
	sc.wait(nodeID, w)
	if until == nil {
		return "", false, workflow.Await(ctx, ready)
	}
	sc.noteResume(nodeID, func(s *stepResume) { s.Until = until })
	timeout := until.Sub(workflow.Now(ctx))
	if timeout <= 0 {
		if ready() {
			return "", false, nil
		}
		return flow.HandleTimeout, true, nil
	}
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
	future, err := r.startChild(ctx, flowID, childID, policy, input)
	if err != nil {
		return nil, err
	}
	var child flow.RunState
	if err := future.Get(ctx, &child); err != nil {
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

// startChild starts a saved flow as a child Workflow.
func (r *flowRun) startChild(ctx workflow.Context, flowID, childID string, policy enumspb.ParentClosePolicy, input any) (workflow.ChildWorkflowFuture, error) {
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
	return workflow.ExecuteChildWorkflow(ctx, FlowWorkflow, flow.RunInput{
		Flow: sub, Input: input, Source: "parent", CallbackBase: r.in.CallbackBase, Subflows: r.in.Subflows,
		SharedLimits: r.in.SharedLimits,
	}), nil
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
