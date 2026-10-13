package workflows

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"

	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/flow"
)

func updateCallback(t *testing.T, onDone func(any, error)) *testsuite.TestUpdateCallback {
	return &testsuite.TestUpdateCallback{
		OnReject:   func(err error) { onDone(nil, err) },
		OnAccept:   func() {},
		OnComplete: onDone,
	}
}

// editableOrder is a small version of the "Editable order" example: a hold
// timer computed from a variable, raced against a cancel condition, with
// lanes that change the variables while it runs.
func editableOrder() flow.Definition {
	return flow.Definition{ID: "f", Name: "editable", Settings: flow.FlowSettings{
		Variables: []flow.VariableDef{
			{Name: "hold_minutes", Initial: `2`, Visible: true},
			{Name: "cancelled", Initial: `false`},
			{Name: "address", Initial: `"old"`},
		},
	}, Nodes: []flow.Node{
		node("start", flow.TypeTrigger, flow.TriggerConfig{Kind: "manual"}),
		node("hold", flow.TypeDelay, flow.DelayConfig{Mode: "expression", Expression: "vars.hold_minutes * 60"}),
		node("cancel_wait", flow.TypeWaitCondition, flow.WaitConditionConfig{Condition: "vars.cancelled == true"}),
		node("race", flow.TypeFirstOf, flow.FirstOfConfig{}),
		node("shipped_to", flow.TypeTransform, flow.TransformConfig{Output: `"{{ vars.address }}"`}),
		node("change_address", flow.TypeOnSignal, flow.OnSignalConfig{SignalName: "change_address"}),
		node("set_address", flow.TypeSetVariable, flow.SetVariableConfig{Assignments: []flow.Assignment{{Name: "address", Value: "{{ steps.change_address.address }}"}}}),
		node("cancel_order", flow.TypeOnSignal, flow.OnSignalConfig{SignalName: "cancel_order"}),
		node("set_cancel", flow.TypeSetVariable, flow.SetVariableConfig{Assignments: []flow.Assignment{{Name: "cancelled", Value: "{{ true }}"}}}),
		node("extend_hold", flow.TypeOnUpdate, flow.OnUpdateConfig{UpdateName: "extend_hold", Validator: "request.minutes > 0 && request.minutes <= 60"}),
		node("set_hold", flow.TypeSetVariable, flow.SetVariableConfig{Assignments: []flow.Assignment{{Name: "hold_minutes", Value: "{{ vars.hold_minutes + steps.extend_hold.minutes }}"}}}),
		node("reply", flow.TypeRespond, flow.RespondConfig{Value: `{"holdMinutes": "{{ vars.hold_minutes }}"}`}),
	}, Edges: []flow.Edge{
		edge("start", "hold", ""), edge("start", "cancel_wait", ""),
		edge("hold", "race", ""), edge("cancel_wait", "race", ""), edge("race", "shipped_to", ""),
		edge("change_address", "set_address", ""), edge("cancel_order", "set_cancel", ""),
		edge("extend_hold", "set_hold", ""), edge("set_hold", "reply", ""),
	}}
}

func TestLanesChangeStateWhileTheMainPathWaits(t *testing.T) {
	def := editableOrder()
	require.Empty(t, def.Validate())
	env := newEnv(t, http.NotFoundHandler())

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("change_address", map[string]any{"address": "1 New St"})
	}, 30*time.Second)
	// At 1 minute, extend the 2-minute hold by 5: the timer recalculates and
	// now fires at 7 minutes. A request the validator rejects changes nothing.
	env.RegisterDelayedCallback(func() {
		env.UpdateWorkflow("extend_hold", "bad", updateCallback(t, func(_ any, err error) {
			require.ErrorContains(t, err, "rejected")
		}), map[string]any{"minutes": 500})
		env.UpdateWorkflow("extend_hold", "good", updateCallback(t, func(v any, err error) {
			require.NoError(t, err)
			require.Equal(t, map[string]any{"holdMinutes": 7.0}, v)
		}), map[string]any{"minutes": 5})
	}, time.Minute)
	env.RegisterDelayedCallback(func() {
		st := queryState(t, env)
		require.Equal(t, flow.NodeWaiting, st.Nodes["hold"].Status, "still holding at 5 minutes")
	}, 5*time.Minute)

	env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{Flow: def})
	require.NoError(t, env.GetWorkflowError())
	st := runState(t, env)
	require.Equal(t, flow.NodeCompleted, st.Nodes["hold"].Status)
	require.Equal(t, flow.NodeSkipped, st.Nodes["cancel_wait"].Status, "the cancel wait lost the race")
	require.Contains(t, st.Nodes["cancel_wait"].Error, "lost the race")
	require.JSONEq(t, `"1 New St"`, string(st.Nodes["shipped_to"].Output))
	require.Equal(t, 1, st.Nodes["change_address"].Invocations)
	require.EqualValues(t, 7, st.Vars["hold_minutes"])
	require.InDelta(t, 7*60, st.EndedAt.Sub(st.StartedAt).Seconds(), 1)
}

func TestCancelSignalWinsTheRace(t *testing.T) {
	env := newEnv(t, http.NotFoundHandler())
	env.RegisterDelayedCallback(func() { env.SignalWorkflow("cancel_order", nil) }, 20*time.Second)

	env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{Flow: editableOrder()})
	require.NoError(t, env.GetWorkflowError())
	st := runState(t, env)
	require.Equal(t, flow.NodeSkipped, st.Nodes["hold"].Status, "the hold timer lost and was cancelled")
	require.JSONEq(t, `{"winner":"cancel_wait","output":{"met":true}}`, string(st.Nodes["race"].Output))
	require.Less(t, st.EndedAt.Sub(st.StartedAt), time.Minute)
}

func TestFailedRunUndoesCompletedStepsInReverse(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	env := newEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls = append(calls, r.URL.Path)
		mu.Unlock()
		switch {
		case r.URL.Path == "/charge":
			w.WriteHeader(http.StatusPaymentRequired)
		case strings.HasSuffix(r.URL.Path, "/cancel"):
			_, _ = w.Write([]byte(`{"status":"cancelled"}`))
		default:
			_, _ = w.Write([]byte(`{"id":"` + strings.TrimPrefix(r.URL.Path, "/") + `-1"}`))
		}
	}))
	def := flow.Definition{ID: "f", Name: "saga", Nodes: []flow.Node{
		node("start", flow.TypeTrigger, flow.TriggerConfig{Kind: "manual"}),
		node("flight", flow.TypeHTTP, flow.HTTPConfig{Method: "POST", URL: baseURL + "/flight",
			Undo: &flow.HTTPRequestSpec{Method: "POST", URL: baseURL + "/{{ steps.flight.body.id }}/cancel"}}),
		node("hotel", flow.TypeHTTP, flow.HTTPConfig{Method: "POST", URL: baseURL + "/hotel",
			Undo: &flow.HTTPRequestSpec{Method: "POST", URL: baseURL + "/{{ steps.hotel.body.id }}/cancel"}}),
		node("charge", flow.TypeHTTP, flow.HTTPConfig{Method: "POST", URL: baseURL + "/charge"}),
	}, Edges: []flow.Edge{edge("start", "flight", ""), edge("flight", "hotel", ""), edge("hotel", "charge", "")}}

	env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{Flow: def})
	require.Error(t, env.GetWorkflowError())
	require.Equal(t, []string{"/flight", "/hotel", "/charge", "/hotel-1/cancel", "/flight-1/cancel"}, calls)
}

func TestNonRetryableStatusCodesStopRetries(t *testing.T) {
	calls := 0
	env := newEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	def := flow.Definition{ID: "f", Name: "status", Nodes: []flow.Node{
		node("start", flow.TypeTrigger, flow.TriggerConfig{Kind: "manual"}),
		node("call", flow.TypeHTTP, flow.HTTPConfig{Method: "GET", URL: baseURL, Activity: flow.ActivityOptions{
			Retry: flow.RetryConfig{MaxAttempts: 5, NonRetryableStatusCodes: []int{503}},
		}}),
	}, Edges: []flow.Edge{edge("start", "call", "")}}

	env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{Flow: def})
	require.Error(t, env.GetWorkflowError())
	require.Equal(t, 1, calls)
}

func TestForEachRunsChildFlowsWithAConcurrencyLimit(t *testing.T) {
	env := newEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	env.RegisterWorkflow(FlowWorkflow)
	child := flow.Definition{ID: "child", Name: "child", Nodes: []flow.Node{
		node("start", flow.TypeTrigger, flow.TriggerConfig{Kind: "manual"}),
		node("pause", flow.TypeDelay, flow.DelayConfig{Mode: "duration", Amount: 1, Unit: "minutes"}),
		node("greet", flow.TypeTransform, flow.TransformConfig{Output: `"hello {{ input.name }}"`}),
	}, Edges: []flow.Edge{edge("start", "pause", ""), edge("pause", "greet", "")}}
	parent := flow.Definition{ID: "parent", Name: "parent", Nodes: []flow.Node{
		node("start", flow.TypeTrigger, flow.TriggerConfig{Kind: "manual"}),
		node("each", flow.TypeForEach, flow.ForEachConfig{Items: "input.names", FlowID: "child", Concurrency: 2, Input: `{"name": "{{ item }}"}`}),
	}, Edges: []flow.Edge{edge("start", "each", "")}}

	env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{
		Flow: parent, Input: map[string]any{"names": []any{"a", "b", "c", "d"}},
		Subflows: map[string]flow.Definition{"child": child},
	})
	require.NoError(t, env.GetWorkflowError())
	st := runState(t, env)
	var out struct {
		Count   int              `json:"count"`
		Results []map[string]any `json:"results"`
	}
	require.NoError(t, json.Unmarshal(st.Nodes["each"].Output, &out))
	require.Equal(t, 4, out.Count)
	require.Equal(t, "hello c", out.Results[2]["greet"])
	require.Equal(t, "4 of 4 done", st.Nodes["each"].Progress)
	// Two at a time, one minute each: two minutes in total, not one or four.
	require.InDelta(t, 120, st.EndedAt.Sub(st.StartedAt).Seconds(), 1)
}

// A keyed flow that isn't an entity completes when its main path does. Here
// the main path holds the run open for 15 minutes; events for the key reach
// it meanwhile.
func TestKeyedRunEndsWithItsMainPath(t *testing.T) {
	env := newEnv(t, http.NotFoundHandler())
	def := flow.Definition{ID: "f", Name: "cart", Settings: flow.FlowSettings{
		KeyedBy:   "input.cartId",
		Variables: []flow.VariableDef{{Name: "items", Initial: `0`}},
	}, Nodes: []flow.Node{
		node("start", flow.TypeTrigger, flow.TriggerConfig{Kind: "manual"}),
		node("hold", flow.TypeDelay, flow.DelayConfig{Mode: "duration", Amount: 15, Unit: "minutes"}),
		node("add_item", flow.TypeOnSignal, flow.OnSignalConfig{SignalName: "add_item"}),
		node("count", flow.TypeSetVariable, flow.SetVariableConfig{Assignments: []flow.Assignment{{Name: "items", Value: "{{ vars.items + 1 }}"}}}),
	}, Edges: []flow.Edge{edge("start", "hold", ""), edge("add_item", "count", "")}}
	require.Empty(t, def.Validate())

	for _, at := range []time.Duration{time.Minute, 5 * time.Minute, 14 * time.Minute} {
		env.RegisterDelayedCallback(func() { env.SignalWorkflow("add_item", map[string]any{}) }, at)
	}
	env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{Flow: def, Key: "cart-7"})
	require.NoError(t, env.GetWorkflowError())
	st := runState(t, env)
	require.EqualValues(t, 3, st.Vars["items"])
	require.InDelta(t, 15*60, st.EndedAt.Sub(st.StartedAt).Seconds(), 1)
}

func TestTimerTargets(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		expr string
		want time.Duration
	}{
		{"90", 90 * time.Second},
		{`duration("2h") * 3`, 6 * time.Hour},
		{`"1d12h"`, 36 * time.Hour},
		{`date("2026-01-02T00:00:00Z")`, 24 * time.Hour},
	} {
		v, err := flow.Eval(tc.expr, flow.Env(nil, nil))
		require.NoError(t, err, tc.expr)
		at, err := flow.TimerTarget(v, start)
		require.NoError(t, err, tc.expr)
		require.Equal(t, tc.want, at.Sub(start), tc.expr)
	}
}

func queryState(t *testing.T, env *testsuite.TestWorkflowEnvironment) flow.RunState {
	t.Helper()
	v, err := env.QueryWorkflow(flow.QueryState)
	require.NoError(t, err)
	var st flow.RunState
	require.NoError(t, v.Get(&st))
	return st
}
