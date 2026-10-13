package workflows

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/flow"
)

// pollFlow polls GET <url> until its status is "ready", pausing 10 seconds
// between passes: the polling pattern, drawn as a loop.
func pollFlow(maxPasses int, withLimit bool) flow.Definition {
	def := flow.Definition{ID: "f", Name: "poll", Nodes: []flow.Node{
		node("start", flow.TypeTrigger, flow.TriggerConfig{Kind: "manual"}),
		node("poll", flow.TypeLoop, flow.LoopConfig{
			Until: `steps.check.body.status == "ready"`, MaxPasses: maxPasses,
			Pause: &flow.DelayConfig{Mode: "duration", Amount: 10, Unit: "seconds"},
		}),
		node("check", flow.TypeHTTP, flow.HTTPConfig{Method: "GET", URL: baseURL + "/status"}),
		node("ready", flow.TypeTransform, flow.TransformConfig{Output: `{"passes": "{{ steps.poll.passes }}"}`}),
	}, Edges: []flow.Edge{
		edge("start", "poll", ""), edge("poll", "check", flow.HandleBody), edge("check", "poll", ""),
		edge("poll", "ready", flow.HandleDone),
	}}
	if withLimit {
		def.Nodes = append(def.Nodes, node("gave_up", flow.TypeTransform, flow.TransformConfig{Output: `{"why": "{{ steps.poll.error }}"}`}))
		def.Edges = append(def.Edges, edge("poll", "gave_up", flow.HandleLimit))
	}
	return def
}

// readyAfter answers {"status":"pending"} until the nth call.
func readyAfter(n int32, calls *atomic.Int32) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		status := "pending"
		if calls.Add(1) >= n {
			status = "ready"
		}
		_, _ = fmt.Fprintf(w, `{"status": %q}`, status)
	})
}

func TestLoopPollsUntilReady(t *testing.T) {
	var calls atomic.Int32
	env := newEnv(t, readyAfter(3, &calls))
	env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{Flow: pollFlow(10, false)})
	require.NoError(t, env.GetWorkflowError())
	st := runState(t, env)

	require.EqualValues(t, 3, calls.Load())
	require.JSONEq(t, `{"passes": 3}`, string(st.Nodes["ready"].Output))
	require.Equal(t, flow.HandleDone, st.Nodes["poll"].Handle)
	require.Equal(t, "3 passes", st.Nodes["poll"].Progress)
	require.InDelta(t, 20, st.EndedAt.Sub(st.StartedAt).Seconds(), 1, "two durable pauses between three passes")
}

func TestLoopTakesItsLimitOutcome(t *testing.T) {
	var calls atomic.Int32
	env := newEnv(t, readyAfter(100, &calls))
	env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{Flow: pollFlow(2, true)})
	require.NoError(t, env.GetWorkflowError())
	st := runState(t, env)
	require.EqualValues(t, 2, calls.Load())
	require.Equal(t, flow.HandleLimit, st.Nodes["poll"].Handle)
	require.Contains(t, string(st.Nodes["gave_up"].Output), "stopped after 2 passes")
}

func TestLoopFailsAtItsLimitWithoutALimitOutcome(t *testing.T) {
	var calls atomic.Int32
	env := newEnv(t, readyAfter(100, &calls))
	env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{Flow: pollFlow(2, false)})
	require.ErrorContains(t, env.GetWorkflowError(), "stopped after 2 passes")
}

// The batch iterator: page through a dataset with a cursor kept in a variable.
func TestLoopPagesWithACursor(t *testing.T) {
	env := newEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next := map[string]string{"": "p2", "p2": "p3", "p3": ""}[r.URL.Query().Get("cursor")]
		_, _ = fmt.Fprintf(w, `{"items": [1, 2, 3], "next": %q}`, next)
	}))
	def := flow.Definition{ID: "f", Name: "pages", Settings: flow.FlowSettings{Variables: []flow.VariableDef{
		{Name: "cursor", Initial: `""`}, {Name: "total", Initial: `0`},
	}}, Nodes: []flow.Node{
		node("start", flow.TypeTrigger, flow.TriggerConfig{Kind: "manual"}),
		node("pages", flow.TypeLoop, flow.LoopConfig{Until: `vars.cursor == ""`, MaxPasses: 10}),
		node("page", flow.TypeHTTP, flow.HTTPConfig{Method: "GET", URL: baseURL + "/records?cursor={{ vars.cursor }}"}),
		node("tally", flow.TypeSetVariable, flow.SetVariableConfig{Assignments: []flow.Assignment{
			{Name: "cursor", Value: "{{ steps.page.body.next }}"},
			{Name: "total", Value: "{{ vars.total + len(steps.page.body.items) }}"},
		}}),
	}, Edges: []flow.Edge{
		edge("start", "pages", ""), edge("pages", "page", flow.HandleBody), edge("page", "tally", ""), edge("tally", "pages", ""),
	}}
	env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{Flow: def})
	require.NoError(t, env.GetWorkflowError())
	st := runState(t, env)
	require.EqualValues(t, 9, st.Vars["total"])
	require.Equal(t, "3 passes", st.Nodes["pages"].Progress)
}

func TestLoopValidation(t *testing.T) {
	def := pollFlow(5, false)
	def.Nodes, def.Edges = def.Nodes[:3], def.Edges[:3] // nothing after the loop is fine
	require.Empty(t, def.Validate())

	open := pollFlow(5, false)
	open.Edges = append(open.Edges[:2], open.Edges[3]) // drop check -> poll
	require.NotEmpty(t, open.Validate(), "a body must lead back into its loop")

	leaky := pollFlow(5, false)
	leaky.Edges = append(leaky.Edges, edge("start", "check", ""))
	require.NotEmpty(t, leaky.Validate(), "a body can only be entered through the loop")

	cyclic := pollFlow(5, false)
	cyclic.Edges = append(cyclic.Edges, edge("ready", "start", ""))
	require.NotEmpty(t, cyclic.Validate())
}

func TestCollectBatchesEventsUntilQuiet(t *testing.T) {
	env := newEnv(t, http.NotFoundHandler())
	def := flow.Definition{ID: "f", Name: "collect", Nodes: []flow.Node{
		node("start", flow.TypeTrigger, flow.TriggerConfig{Kind: "manual"}),
		node("batch", flow.TypeWaitSignal, flow.WaitSignalConfig{SignalName: "item_added", Collect: &flow.CollectConfig{
			QuietSeconds: 10, DedupeBy: "event.sku",
		}}),
	}, Edges: []flow.Edge{edge("start", "batch", "")}}
	for i, sku := range []string{"a", "b", "a"} {
		env.RegisterDelayedCallback(func() {
			env.SignalWorkflow("item_added", map[string]any{"sku": sku})
		}, time.Duration(i+1)*time.Second)
	}
	env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{Flow: def})
	require.NoError(t, env.GetWorkflowError())
	st := runState(t, env)
	var out struct {
		Count      int `json:"count"`
		Duplicates int `json:"duplicates"`
	}
	require.NoError(t, json.Unmarshal(st.Nodes["batch"].Output, &out))
	require.Equal(t, 2, out.Count)
	require.Equal(t, 1, out.Duplicates)
	require.InDelta(t, 13, st.EndedAt.Sub(st.StartedAt).Seconds(), 1, "ten quiet seconds after the last event")
}

// A resumable Activity: retries run out, a person corrects the body, the
// step runs again and succeeds.
func TestPausedStepResumesWithAFix(t *testing.T) {
	env := newEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "12345") {
			w.WriteHeader(http.StatusUnprocessableEntity)
			return
		}
		_, _ = w.Write([]byte(`{"valid": true}`))
	}))
	def := flow.Definition{ID: "f", Name: "fix", Nodes: []flow.Node{
		node("start", flow.TypeTrigger, flow.TriggerConfig{Kind: "manual"}),
		node("validate", flow.TypeHTTP, flow.HTTPConfig{Method: "POST", URL: baseURL, Body: `{"zip": "0000"}`, PauseForFix: true}),
	}, Edges: []flow.Edge{edge("start", "validate", "")}}
	var updateErr error
	env.RegisterDelayedCallback(func() {
		fixed := `{"zip": "12345"}`
		env.UpdateWorkflow(flow.UpdateFix, "fix-1", updateCallback(t, func(_ any, err error) { updateErr = err }),
			flow.FixDecision{NodeID: "validate", Action: "retry", Body: &fixed})
	}, time.Hour)
	env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{Flow: def})
	require.NoError(t, env.GetWorkflowError())
	require.NoError(t, updateErr)
	st := runState(t, env)
	require.Equal(t, flow.NodeCompleted, st.Nodes["validate"].Status)
	require.Equal(t, "retrying after a fix", st.Nodes["validate"].Progress)
}

func TestFastThenSlowRetries(t *testing.T) {
	var calls atomic.Int32
	env := newEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= 5 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	def := flow.Definition{ID: "f", Name: "slow", Nodes: []flow.Node{
		node("start", flow.TypeTrigger, flow.TriggerConfig{Kind: "manual"}),
		node("call", flow.TypeHTTP, flow.HTTPConfig{Method: "GET", URL: baseURL, Activity: flow.ActivityOptions{
			Retry:     flow.RetryConfig{MaxAttempts: 2, InitialIntervalSeconds: 1, BackoffCoefficient: 1},
			SlowRetry: &flow.RetryConfig{InitialIntervalSeconds: 60, BackoffCoefficient: 1},
		}}),
	}, Edges: []flow.Edge{edge("start", "call", "")}}
	env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{Flow: def})
	require.NoError(t, env.GetWorkflowError())
	st := runState(t, env)
	require.EqualValues(t, 6, calls.Load(), "two quick attempts, then four slow ones")
	require.Equal(t, "succeeded on a slow retry", st.Nodes["call"].Progress)
}

func TestRetryAfterSetsTheNextRetryDelay(t *testing.T) {
	require.Equal(t, 30*time.Second, must(retryAfter("30", time.Now())))
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	require.Equal(t, 2*time.Minute, must(retryAfter("Thu, 01 Jan 2026 12:02:00 GMT", now)))
	require.Equal(t, maxRetryAfter, must(retryAfter("86400", now)))
	_, ok := retryAfter("soon", now)
	require.False(t, ok)

	var calls atomic.Int32
	env := newEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "45")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	def := flow.Definition{ID: "f", Name: "429", Nodes: []flow.Node{
		node("start", flow.TypeTrigger, flow.TriggerConfig{Kind: "manual"}),
		node("call", flow.TypeHTTP, flow.HTTPConfig{Method: "GET", URL: baseURL, Activity: flow.ActivityOptions{
			Retry: flow.RetryConfig{MaxAttempts: 3, InitialIntervalSeconds: 1, BackoffCoefficient: 1},
		}}),
	}, Edges: []flow.Edge{edge("start", "call", "")}}
	env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{Flow: def})
	require.NoError(t, env.GetWorkflowError())
	st := runState(t, env)
	require.EqualValues(t, 2, calls.Load())
	require.GreaterOrEqual(t, st.EndedAt.Sub(st.StartedAt), 45*time.Second, "waited as the server asked, not the 1s backoff")
}

func must(d time.Duration, ok bool) time.Duration {
	if !ok {
		return -1
	}
	return d
}

// Early return: the caller gets the Respond value while the run carries on.
func TestEarlyReturnAnswersBeforeTheRunFinishes(t *testing.T) {
	env := newEnv(t, http.NotFoundHandler())
	def := flow.Definition{ID: "f", Name: "early", Nodes: []flow.Node{
		node("start", flow.TypeTrigger, flow.TriggerConfig{Kind: "webhook"}),
		node("accept", flow.TypeRespond, flow.RespondConfig{Value: `{"accepted": true, "order": "{{ input.order }}"}`}),
		node("fulfil", flow.TypeDelay, flow.DelayConfig{Mode: "duration", Amount: 1, Unit: "hours"}),
	}, Edges: []flow.Edge{edge("start", "accept", ""), edge("accept", "fulfil", "")}}
	require.Empty(t, def.Validate())
	require.True(t, def.RespondsEarly())

	var answer any
	var answeredAt time.Time
	env.RegisterDelayedCallback(func() {
		env.UpdateWorkflow(flow.UpdateEarlyResponse, "early", updateCallback(t, func(v any, err error) {
			require.NoError(t, err)
			answer, answeredAt = v, env.Now()
		}))
	}, 0)
	start := env.Now()
	env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{Flow: def, Input: map[string]any{"order": "A-1"}})
	require.NoError(t, env.GetWorkflowError())
	require.Equal(t, map[string]any{"accepted": true, "order": "A-1"}, answer)
	require.Less(t, answeredAt.Sub(start), time.Minute, "answered right away")
	st := runState(t, env)
	require.GreaterOrEqual(t, st.EndedAt.Sub(st.StartedAt), time.Hour, "and the run kept going")
}
