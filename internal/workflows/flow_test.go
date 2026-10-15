package workflows

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"

	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/flow"
)

func node(id string, t flow.NodeType, config any) flow.Node {
	raw, _ := json.Marshal(config)
	return flow.Node{ID: id, Type: t, Key: id, Label: id, Config: raw}
}

func edge(from, to, handle string) flow.Edge {
	return flow.Edge{ID: from + "-" + to, Source: from, Target: to, SourceHandle: handle}
}

func newEnv(t *testing.T, handler http.Handler) *testsuite.TestWorkflowEnvironment {
	t.Helper()
	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	env.RegisterActivity(&Activities{HTTP: srv.Client()})
	env.RegisterWorkflow(AgentWorkflow)
	env.SetTestTimeout(10 * time.Second)
	t.Cleanup(func() { env.AssertExpectations(t) })
	// Tests reference the server through this placeholder in URLs.
	baseURL = srv.URL
	return env
}

var baseURL string

func runState(t *testing.T, env *testsuite.TestWorkflowEnvironment) flow.RunState {
	t.Helper()
	var st flow.RunState
	require.NoError(t, env.GetWorkflowResult(&st))
	return st
}

func TestBranchingSkipsTheUntakenPath(t *testing.T) {
	env := newEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"shipped","total":120}`))
	}))
	def := flow.Definition{ID: "f", Name: "branch", Nodes: []flow.Node{
		node("start", flow.TypeTrigger, flow.TriggerConfig{Kind: "manual"}),
		node("order", flow.TypeHTTP, flow.HTTPConfig{Method: "GET", URL: baseURL + "/orders/{{ input.id }}", Activity: flow.ActivityOptions{Retry: flow.RetryConfig{MaxAttempts: 1}}}),
		node("big", flow.TypeCondition, flow.ConditionConfig{Expression: "steps.order.body.total > 100"}),
		node("vip", flow.TypeTransform, flow.TransformConfig{Output: `{"msg":"VIP order {{ input.id }}","total":"{{ steps.order.body.total }}"}`}),
		node("normal", flow.TypeTransform, flow.TransformConfig{Output: `{"msg":"normal"}`}),
	}, Edges: []flow.Edge{
		edge("start", "order", ""), edge("order", "big", ""),
		edge("big", "vip", flow.HandleTrue), edge("big", "normal", flow.HandleFalse),
	}}
	require.Empty(t, def.Validate())

	env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{Flow: def, Input: map[string]any{"id": "A1"}})
	require.NoError(t, env.GetWorkflowError())
	st := runState(t, env)

	require.Equal(t, flow.RunCompleted, st.Status)
	require.Equal(t, flow.HandleTrue, st.Nodes["big"].Handle)
	require.Equal(t, flow.NodeSkipped, st.Nodes["normal"].Status)
	// A single-placeholder template keeps the value's type.
	require.JSONEq(t, `{"msg":"VIP order A1","total":120}`, string(st.Nodes["vip"].Output))
}

func TestHTTPRetriesThenRoutesToErrorHandle(t *testing.T) {
	var calls atomic.Int32
	env := newEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	def := flow.Definition{ID: "f", Name: "errors", Nodes: []flow.Node{
		node("start", flow.TypeTrigger, flow.TriggerConfig{Kind: "manual"}),
		node("call", flow.TypeHTTP, flow.HTTPConfig{Method: "POST", URL: baseURL, Activity: flow.ActivityOptions{Retry: flow.RetryConfig{MaxAttempts: 3, InitialIntervalSeconds: 1, BackoffCoefficient: 1}}}),
		node("fallback", flow.TypeTransform, flow.TransformConfig{Output: `{"handled":"{{ steps.call.error }}"}`}),
	}, Edges: []flow.Edge{edge("start", "call", ""), edge("call", "fallback", flow.HandleError)}}

	env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{Flow: def})
	require.NoError(t, env.GetWorkflowError())
	st := runState(t, env)

	require.EqualValues(t, 3, calls.Load(), "Temporal's retry policy makes every attempt")
	require.Equal(t, flow.HandleError, st.Nodes["call"].Handle)
	require.Contains(t, string(st.Nodes["fallback"].Output), "503")
}

func TestClientErrorsAreNotRetried(t *testing.T) {
	var calls atomic.Int32
	env := newEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	def := flow.Definition{ID: "f", Name: "404", Nodes: []flow.Node{
		node("start", flow.TypeTrigger, flow.TriggerConfig{Kind: "manual"}),
		node("call", flow.TypeHTTP, flow.HTTPConfig{Method: "GET", URL: baseURL, Activity: flow.ActivityOptions{Retry: flow.RetryConfig{MaxAttempts: 5, InitialIntervalSeconds: 1, BackoffCoefficient: 1}}}),
	}, Edges: []flow.Edge{edge("start", "call", "")}}

	env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{Flow: def})
	require.Error(t, env.GetWorkflowError())
	require.EqualValues(t, 1, calls.Load())
}

func TestWaitForConditionReactsToSignals(t *testing.T) {
	env := newEnv(t, http.NotFoundHandler())
	def := flow.Definition{ID: "f", Name: "condition", Nodes: []flow.Node{
		node("start", flow.TypeTrigger, flow.TriggerConfig{Kind: "manual"}),
		node("hold", flow.TypeDelay, flow.DelayConfig{Seconds: 60}),
		node("shipped", flow.TypeWaitCondition, flow.WaitConditionConfig{
			SignalName: "order_status", Condition: `events.order_status.status == "shipped"`, TimeoutSeconds: 3600,
		}),
	}, Edges: []flow.Edge{edge("start", "hold", ""), edge("hold", "shipped", "")}}

	// "paid" arrives while the run is still in the delay, before the wait
	// step starts; it is recorded but doesn't satisfy the condition.
	env.RegisterDelayedCallback(func() { env.SignalWorkflow("order_status", map[string]any{"status": "paid"}) }, 10*time.Second)
	env.RegisterDelayedCallback(func() {
		v, err := env.QueryWorkflow(flow.QueryState)
		require.NoError(t, err)
		var st flow.RunState
		require.NoError(t, v.Get(&st))
		require.Equal(t, flow.NodeWaiting, st.Nodes["shipped"].Status)
		require.Equal(t, 1, st.Events["order_status"].Count)
		env.SignalWorkflow("order_status", map[string]any{"status": "packed"})
	}, 2*time.Minute)
	env.RegisterDelayedCallback(func() { env.SignalWorkflow("order_status", map[string]any{"status": "shipped"}) }, 3*time.Minute)

	env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{Flow: def, CallbackBase: "http://app/hooks/tok"})
	require.NoError(t, env.GetWorkflowError())
	st := runState(t, env)
	require.Equal(t, flow.HandleOut, st.Nodes["shipped"].Handle)
	require.Equal(t, 3, st.Events["order_status"].Count)
	require.Equal(t, "http://app/hooks/tok/runs/default-test-workflow-id", st.CallbackURL)
	require.JSONEq(t, `{"met":true,"event":{"status":"shipped"}}`, string(st.Nodes["shipped"].Output))
}

func TestWaitForConditionTimesOut(t *testing.T) {
	env := newEnv(t, http.NotFoundHandler())
	def := flow.Definition{ID: "f", Name: "timeout", Nodes: []flow.Node{
		node("start", flow.TypeTrigger, flow.TriggerConfig{Kind: "manual"}),
		node("shipped", flow.TypeWaitCondition, flow.WaitConditionConfig{
			SignalName: "order_status", Condition: `events.order_status.status == "shipped"`, TimeoutSeconds: 300,
		}),
	}, Edges: []flow.Edge{edge("start", "shipped", "")}}

	env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{Flow: def})
	require.NoError(t, env.GetWorkflowError())
	require.Equal(t, flow.HandleTimeout, runState(t, env).Nodes["shipped"].Handle)
}

func TestApprovalUpdateAndValidator(t *testing.T) {
	env := newEnv(t, http.NotFoundHandler())
	def := flow.Definition{ID: "f", Name: "approve", Nodes: []flow.Node{
		node("start", flow.TypeTrigger, flow.TriggerConfig{Kind: "manual"}),
		node("ok", flow.TypeApproval, flow.ApprovalConfig{Title: "Refund {{ input.amount }}?", TimeoutSeconds: 3600}),
		node("yes", flow.TypeTransform, flow.TransformConfig{Output: `"refunded"`}),
		node("no", flow.TypeTransform, flow.TransformConfig{Output: `"declined"`}),
	}, Edges: []flow.Edge{edge("start", "ok", ""), edge("ok", "yes", flow.HandleApproved), edge("ok", "no", flow.HandleRejected)}}

	env.RegisterDelayedCallback(func() {
		var st flow.RunState
		v, err := env.QueryWorkflow(flow.QueryState)
		require.NoError(t, err)
		require.NoError(t, v.Get(&st))
		require.Equal(t, flow.NodeWaiting, st.Nodes["ok"].Status)
		require.Equal(t, "Refund 250?", st.Nodes["ok"].Waiting.Title)

		// A decision for a node that isn't waiting is rejected by the validator.
		env.UpdateWorkflow(flow.UpdateApprove, "bad", &testsuite.TestUpdateCallback{
			OnReject:   func(err error) {},
			OnAccept:   func() { t.Error("update for the wrong node was accepted") },
			OnComplete: func(any, error) {},
		}, flow.ApprovalDecision{NodeID: "yes", Approved: true})

		env.UpdateWorkflow(flow.UpdateApprove, "good", &testsuite.TestUpdateCallback{
			OnReject:   func(err error) { t.Errorf("valid approval rejected: %v", err) },
			OnAccept:   func() {},
			OnComplete: func(any, error) {},
		}, flow.ApprovalDecision{NodeID: "ok", Approved: true, By: "presenter"})
	}, time.Minute)

	env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{Flow: def, Input: map[string]any{"amount": 250}})
	require.NoError(t, env.GetWorkflowError())
	st := runState(t, env)
	require.Equal(t, flow.HandleApproved, st.Nodes["ok"].Handle)
	require.Equal(t, flow.NodeSkipped, st.Nodes["no"].Status)
}

func TestApprovalTimeoutIsNotARejection(t *testing.T) {
	env := newEnv(t, http.NotFoundHandler())
	def := flow.Definition{ID: "f", Name: "timeout", Nodes: []flow.Node{
		node("start", flow.TypeTrigger, flow.TriggerConfig{Kind: "manual"}),
		node("ok", flow.TypeApproval, flow.ApprovalConfig{Title: "Approve?", TimeoutSeconds: 600}),
	}, Edges: []flow.Edge{edge("start", "ok", "")}}

	env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{Flow: def})
	require.NoError(t, env.GetWorkflowError())
	require.Equal(t, flow.HandleTimeout, runState(t, env).Nodes["ok"].Handle)
}

func TestParallelBranchesJoin(t *testing.T) {
	env := newEnv(t, http.NotFoundHandler())
	def := flow.Definition{ID: "f", Name: "join", Nodes: []flow.Node{
		node("start", flow.TypeTrigger, flow.TriggerConfig{Kind: "manual"}),
		node("payment", flow.TypeWaitSignal, flow.WaitSignalConfig{SignalName: "payment_received"}),
		node("cool_off", flow.TypeDelay, flow.DelayConfig{Seconds: 30}),
		node("done", flow.TypeTransform, flow.TransformConfig{Output: `{"paid":"{{ steps.payment.payload.amount }}"}`}),
	}, Edges: []flow.Edge{
		edge("start", "payment", ""), edge("start", "cool_off", ""),
		edge("payment", "done", flow.HandleReceived), edge("cool_off", "done", ""),
	}}
	// Sent at 10s while the 30s cool-off is still running: the event waits
	// for the step rather than being lost.
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("payment_received", map[string]any{"amount": 42})
	}, 10*time.Second)

	env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{Flow: def})
	require.NoError(t, env.GetWorkflowError())
	st := runState(t, env)
	require.JSONEq(t, `{"paid":42}`, string(st.Nodes["done"].Output))
}

func TestLibraryRejectsInvalidFlows(t *testing.T) {
	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()
	valid := flow.Definition{ID: "f1", Name: "ok", Nodes: []flow.Node{
		node("start", flow.TypeTrigger, flow.TriggerConfig{Kind: "manual"}),
	}}
	invalid := flow.Definition{ID: "f2", Name: "no trigger"}

	env.RegisterDelayedCallback(func() {
		env.UpdateWorkflow(UpdateSaveFlow, "1", &testsuite.TestUpdateCallback{
			OnReject:   func(err error) { t.Errorf("valid flow rejected: %v", err) },
			OnAccept:   func() {},
			OnComplete: func(v any, err error) { require.Equal(t, 1, v.(flow.Definition).Version) },
		}, valid)
		env.UpdateWorkflow(UpdateSaveFlow, "2", &testsuite.TestUpdateCallback{
			OnReject:   func(err error) {},
			OnAccept:   func() { t.Error("invalid flow accepted") },
			OnComplete: func(any, error) {},
		}, invalid)
	}, time.Second)
	env.RegisterDelayedCallback(func() {
		v, err := env.QueryWorkflow(QueryFlows)
		require.NoError(t, err)
		var flows []flow.Definition
		require.NoError(t, v.Get(&flows))
		require.Len(t, flows, 1)
	}, 2*time.Second)

	env.ExecuteWorkflow(LibraryWorkflow, LibraryInput{IdleTimeoutSeconds: 3600})
	// The library closes itself once the session goes idle.
	require.NoError(t, env.GetWorkflowError())
}

// The UI names a new flow itself, so creating must never overwrite one.
func TestLibraryCreateRefusesAnIDInUse(t *testing.T) {
	var s testsuite.WorkflowTestSuite
	env := s.NewTestWorkflowEnvironment()
	def := flow.Definition{ID: "f-abc123", Name: "new", Nodes: []flow.Node{
		node("start", flow.TypeTrigger, flow.TriggerConfig{Kind: "manual"}),
	}}
	again := def
	again.Name = "same id"

	var created flow.Definition
	var refused error
	env.RegisterDelayedCallback(func() {
		env.UpdateWorkflow(UpdateCreateFlow, "1", &testsuite.TestUpdateCallback{
			OnReject:   func(err error) { t.Errorf("new flow refused: %v", err) },
			OnAccept:   func() {},
			OnComplete: func(v any, err error) { created = v.(flow.Definition) },
		}, def)
		env.UpdateWorkflow(UpdateCreateFlow, "2", &testsuite.TestUpdateCallback{
			OnReject:   func(err error) { refused = err },
			OnAccept:   func() { t.Error("create overwrote a flow") },
			OnComplete: func(any, error) {},
		}, again)
	}, time.Second)
	env.RegisterDelayedCallback(func() {
		v, err := env.QueryWorkflow(QueryFlows)
		require.NoError(t, err)
		var flows []flow.Definition
		require.NoError(t, v.Get(&flows))
		require.Len(t, flows, 1)
		require.Equal(t, "new", flows[0].Name)
	}, 2*time.Second)

	env.ExecuteWorkflow(LibraryWorkflow, LibraryInput{IdleTimeoutSeconds: 3600})
	require.NoError(t, env.GetWorkflowError())
	require.Equal(t, 1, created.Version)
	var appErr *temporal.ApplicationError
	require.ErrorAs(t, refused, &appErr)
	require.Equal(t, "FlowExists", appErr.Type())
}

func TestAgentLoopUsesToolsAndAsksAHuman(t *testing.T) {
	env := newEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"C-42","tier":"gold"}`))
	}))
	var a *Activities
	// A scripted model: look up the customer, ask a person, then answer.
	turns := []LLMResponse{
		{StopReason: "tool_use", Assistant: json.RawMessage(`{"role":"assistant","content":[]}`),
			ToolCalls: []ToolCall{{ID: "t1", Name: "get_customer", Input: json.RawMessage(`{"customer_id":"C-42"}`)}}},
		{StopReason: "tool_use", Assistant: json.RawMessage(`{"role":"assistant","content":[]}`),
			ToolCalls: []ToolCall{{ID: "t2", Name: askHumanTool, Input: json.RawMessage(`{"question":"Refund $180?"}`)}}},
		{StopReason: "end_turn", Assistant: json.RawMessage(`{"role":"assistant","content":[]}`),
			Text: "```json\n{\"action\":\"refund\",\"refundAmount\":180}\n```"},
	}
	calls := 0
	env.OnActivity(a.CallClaude, mock.Anything, mock.Anything).Return(
		func(_ context.Context, req LLMRequest) (LLMResponse, error) {
			calls++
			// Each turn sends the whole conversation so far.
			require.Len(t, req.Messages, 2*calls-1)
			return turns[calls-1], nil
		})
	env.RegisterDelayedCallback(func() {
		env.UpdateWorkflow(flow.UpdateAnswer, "a", &testsuite.TestUpdateCallback{
			OnReject: func(err error) { t.Errorf("answer rejected: %v", err) }, OnAccept: func() {}, OnComplete: func(any, error) {},
		}, "Yes, approved")
	}, time.Minute)

	env.ExecuteWorkflow(AgentWorkflow, AgentInput{
		Instructions: "triage", MaxTurns: 5, AllowAskHuman: true,
		Tools: []flow.AgentTool{{Name: "get_customer", Description: "d", Method: "GET", URL: baseURL + "/customers/{{ args.customer_id }}",
			Params: []flow.AgentToolParam{{Name: "customer_id"}}}},
	})
	require.NoError(t, env.GetWorkflowError())
	var res AgentResult
	require.NoError(t, env.GetWorkflowResult(&res))
	require.Equal(t, 3, res.Turns)
	require.Equal(t, map[string]any{"action": "refund", "refundAmount": 180.0}, res.Data)
}
