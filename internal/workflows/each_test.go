package workflows

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/converter"

	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/flow"
)

// prices answers GET /price/<sku> with a price; "bad" skus get a 404.
func prices(calls *[]string, mu *sync.Mutex) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sku := strings.TrimPrefix(r.URL.Path, "/price/")
		mu.Lock()
		*calls = append(*calls, sku)
		mu.Unlock()
		if strings.HasPrefix(sku, "bad") {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error": "no such sku"}`))
			return
		}
		_, _ = fmt.Fprintf(w, `{"sku": %q, "price": %d}`, sku, len(sku)*10)
	})
}

// inlineEach prices each sku inside the run: wait a minute, fetch the price,
// and label it cheap or dear.
func inlineEach(c flow.ForEachConfig) flow.Definition {
	c.Run, c.Items = flow.ForEachRunSteps, "input.skus"
	if c.Concurrency == 0 {
		c.Concurrency = 2
	}
	return flow.Definition{ID: "f", Name: "price all", Nodes: []flow.Node{
		node("start", flow.TypeTrigger, flow.TriggerConfig{Kind: "manual"}),
		node("each", flow.TypeForEach, c),
		node("pause", flow.TypeDelay, flow.DelayConfig{Mode: "duration", Amount: 1, Unit: "minutes"}),
		node("fetch", flow.TypeHTTP, flow.HTTPConfig{Method: "GET", URL: baseURL + "/price/{{ item }}",
			Retry: &flow.RetryConfig{MaxAttempts: 1}}),
		node("dear", flow.TypeCondition, flow.ConditionConfig{Expression: "steps.fetch.body.price > 20"}),
		node("label_dear", flow.TypeTransform, flow.TransformConfig{Output: `{"sku": "{{ item }}", "index": "{{ index }}", "label": "dear"}`}),
		node("label_cheap", flow.TypeTransform, flow.TransformConfig{Output: `{"sku": "{{ item }}", "label": "cheap", "batch": "{{ input.batch }}"}`}),
		node("report", flow.TypeTransform, flow.TransformConfig{Output: `{"count": "{{ steps.each.count }}"}`}),
	}, Edges: []flow.Edge{
		edge("start", "each", ""),
		edge("each", "pause", flow.HandleBody), edge("pause", "fetch", ""), edge("fetch", "dear", ""),
		edge("dear", "label_dear", flow.HandleTrue), edge("dear", "label_cheap", flow.HandleFalse),
		edge("label_dear", "each", ""), edge("label_cheap", "each", ""),
		edge("each", "report", ""),
	}}
}

func TestForEachRunsItsBodyInsideTheRun(t *testing.T) {
	var calls []string
	var mu sync.Mutex
	env := newEnv(t, prices(&calls, &mu))
	def := inlineEach(flow.ForEachConfig{})
	require.Empty(t, def.Validate())
	env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{Flow: def, Input: map[string]any{
		"skus": []any{"a", "bb", "ccc", "dddd", "e", "ff"}, "batch": "B1",
	}})
	require.NoError(t, env.GetWorkflowError())
	st := runState(t, env)

	var out struct {
		Count     int              `json:"count"`
		Succeeded int              `json:"succeeded"`
		Results   []map[string]any `json:"results"`
	}
	require.NoError(t, json.Unmarshal(st.Nodes["each"].Output, &out))
	require.Equal(t, 6, out.Count)
	require.Equal(t, 6, out.Succeeded)
	// Each item has its own outputs, and each took its own branch.
	require.Equal(t, map[string]any{"sku": "a", "label": "cheap", "batch": "B1"}, out.Results[0]["label_cheap"])
	require.NotContains(t, out.Results[0], "label_dear")
	require.Equal(t, map[string]any{"sku": "ccc", "index": float64(2), "label": "dear"}, out.Results[2]["label_dear"])
	require.Equal(t, float64(40), out.Results[3]["fetch"].(map[string]any)["body"].(map[string]any)["price"])
	require.Len(t, calls, 6)

	// Two at a time, a minute each: three minutes, and no child Workflows.
	require.InDelta(t, 180, st.EndedAt.Sub(st.StartedAt).Seconds(), 1)
	require.Empty(t, st.Nodes["fetch"].ChildWorkflowID)

	// Body steps summarise every item.
	require.Equal(t, flow.NodeCompleted, st.Nodes["fetch"].Status)
	require.Equal(t, "6 done", st.Nodes["fetch"].Progress)
	require.Equal(t, "2 done · 4 skipped", st.Nodes["label_dear"].Progress)
	require.JSONEq(t, `{"count": 6}`, string(st.Nodes["report"].Output))

	// The run keeps each item's own step states.
	items := st.Items["each"]
	require.Len(t, items, 6)
	require.Equal(t, 1, items[1].Index)
	require.JSONEq(t, `"bb"`, string(items[1].Item))
	require.Equal(t, flow.NodeCompleted, items[1].Status)
	require.Equal(t, flow.NodeSkipped, items[1].Nodes["label_dear"].Status)
	require.Equal(t, flow.HandleFalse, items[1].Nodes["dear"].Handle)
}

// With every item required, a failed item fails the step and cancels the
// rest; an error outcome inside the body handles it per item instead.
func TestForEachBodyFailures(t *testing.T) {
	t.Run("unhandled failure fails the step", func(t *testing.T) {
		var calls []string
		var mu sync.Mutex
		env := newEnv(t, prices(&calls, &mu))
		env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{Flow: inlineEach(flow.ForEachConfig{Concurrency: 1}), Input: map[string]any{
			"skus": []any{"a", "bad1", "c"},
		}})
		require.Error(t, env.GetWorkflowError())
		require.Contains(t, env.GetWorkflowError().Error(), "404")
		require.Equal(t, []string{"a", "bad1"}, calls, "the last item never started")
	})

	t.Run("error outcome in the body", func(t *testing.T) {
		var calls []string
		var mu sync.Mutex
		env := newEnv(t, prices(&calls, &mu))
		def := inlineEach(flow.ForEachConfig{})
		def.Nodes = append(def.Nodes, node("missing", flow.TypeTransform, flow.TransformConfig{Output: `{"missing": "{{ item }}", "why": "{{ steps.fetch.error }}"}`}))
		def.Edges = append(def.Edges, edge("fetch", "missing", flow.HandleError), edge("missing", "each", ""))
		require.Empty(t, def.Validate())
		env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{Flow: def, Input: map[string]any{"skus": []any{"a", "bad1", "ccc"}}})
		require.NoError(t, env.GetWorkflowError())
		st := runState(t, env)
		var out struct {
			Results []map[string]any `json:"results"`
		}
		require.NoError(t, json.Unmarshal(st.Nodes["each"].Output, &out))
		require.Equal(t, "bad1", out.Results[1]["missing"].(map[string]any)["missing"])
		require.Contains(t, out.Results[1]["missing"].(map[string]any)["why"], "404")
		require.Contains(t, out.Results[2], "label_dear")
		require.Equal(t, "1 done · 2 skipped", st.Nodes["missing"].Progress)
	})

	t.Run("a quorum tolerates failed items", func(t *testing.T) {
		var calls []string
		var mu sync.Mutex
		env := newEnv(t, prices(&calls, &mu))
		def := inlineEach(flow.ForEachConfig{Concurrency: 1, FinishWhen: flow.ForEachCount, Count: 2})
		env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{Flow: def, Input: map[string]any{"skus": []any{"bad1", "a", "bad2", "b", "c"}}})
		require.NoError(t, env.GetWorkflowError())
		st := runState(t, env)
		var out struct {
			Winners []map[string]any `json:"winners"`
			Failed  int              `json:"failed"`
		}
		require.NoError(t, json.Unmarshal(st.Nodes["each"].Output, &out))
		require.Len(t, out.Winners, 2)
		require.Equal(t, 2, out.Failed)
		require.Equal(t, []string{"bad1", "a", "bad2", "b"}, calls, "stops starting items once two have succeeded")
		require.Equal(t, flow.NodeFailed, st.Items["each"][0].Status)
	})
}

// A start rate spaces items out with a durable timer, even when the window
// has room.
func TestForEachStartRate(t *testing.T) {
	def := flow.Definition{ID: "f", Name: "paced", Nodes: []flow.Node{
		node("start", flow.TypeTrigger, flow.TriggerConfig{Kind: "manual"}),
		node("each", flow.TypeForEach, flow.ForEachConfig{Run: flow.ForEachRunSteps, Items: "input.n", Concurrency: 20,
			Rate: &flow.RateLimit{Count: 2, Per: "minute"}}),
		node("stamp", flow.TypeTransform, flow.TransformConfig{Output: `{"i": "{{ index }}"}`}),
	}, Edges: []flow.Edge{edge("start", "each", ""), edge("each", "stamp", flow.HandleBody), edge("stamp", "each", "")}}
	require.Empty(t, def.Validate())
	env := newEnv(t, http.NotFoundHandler())
	env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{Flow: def, Input: map[string]any{"n": []any{1, 2, 3, 4, 5}}})
	require.NoError(t, env.GetWorkflowError())
	st := runState(t, env)
	// Five starts, 30 seconds apart: the last starts two minutes in.
	require.InDelta(t, 120, st.EndedAt.Sub(st.StartedAt).Seconds(), 1)
}

// Like children, items running at a hand-off finish first; the rest start
// in the next run.
func TestForEachBodyContinuesAsNewPartWay(t *testing.T) {
	var calls []string
	var mu sync.Mutex
	env := newEnv(t, prices(&calls, &mu))
	in := flow.RunInput{Flow: inlineEach(flow.ForEachConfig{}), Input: map[string]any{"skus": []any{"a", "bb", "ccc", "dddd", "e", "ff"}}}
	suggestContinueAsNewAt(env, 90*time.Second)
	env.ExecuteWorkflow(FlowWorkflow, in)
	next := continuedAsNew(t, env)

	env = newEnv(t, prices(&calls, &mu))
	env.ExecuteWorkflow(FlowWorkflow, next)
	require.NoError(t, env.GetWorkflowError())
	st := runState(t, env)
	var out struct {
		Succeeded int              `json:"succeeded"`
		Results   []map[string]any `json:"results"`
	}
	require.NoError(t, json.Unmarshal(st.Nodes["each"].Output, &out))
	require.Equal(t, 6, out.Succeeded)
	require.Contains(t, out.Results[0], "label_cheap", "results from the first run carried over")
	require.Contains(t, out.Results[5], "label_cheap")
	slices.Sort(calls)
	require.Equal(t, []string{"a", "bb", "ccc", "dddd", "e", "ff"}, calls, "each item ran exactly once")
	require.InDelta(t, 60, st.EndedAt.Sub(st.StartedAt).Seconds(), 1, "the second run had two items left")
}

// A Loop that pages, fanning each page out inline: the body runs again on
// every pass.
func TestForEachBodyInsideALoop(t *testing.T) {
	def := flow.Definition{ID: "f", Name: "pages", Settings: flow.FlowSettings{
		Variables: []flow.VariableDef{{Name: "page", Initial: "0"}, {Name: "seen", Initial: "0"}},
	}, Nodes: []flow.Node{
		node("start", flow.TypeTrigger, flow.TriggerConfig{Kind: "manual"}),
		node("pages", flow.TypeLoop, flow.LoopConfig{Until: "vars.page == 3", MaxPasses: 10}),
		node("each", flow.TypeForEach, flow.ForEachConfig{Run: flow.ForEachRunSteps, Items: "[vars.page, vars.page, vars.page]", Concurrency: 3}),
		node("count", flow.TypeSetVariable, flow.SetVariableConfig{Assignments: []flow.Assignment{{Name: "seen", Value: "{{ vars.seen + 1 }}"}}}),
		node("turn", flow.TypeSetVariable, flow.SetVariableConfig{Assignments: []flow.Assignment{{Name: "page", Value: "{{ vars.page + 1 }}"}}}),
	}, Edges: []flow.Edge{
		edge("start", "pages", ""), edge("pages", "each", flow.HandleBody),
		edge("each", "count", flow.HandleBody), edge("count", "each", ""),
		edge("each", "turn", ""), edge("turn", "pages", ""),
	}}
	require.Empty(t, def.Validate())
	env := newEnv(t, http.NotFoundHandler())
	env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{Flow: def})
	require.NoError(t, env.GetWorkflowError())
	st := runState(t, env)
	require.EqualValues(t, 9, st.Vars["seen"])
	require.Equal(t, "3 done", st.Nodes["count"].Progress, "the last pass's items")
}

// A Run flow step in a body starts one child per item.
func TestForEachBodyRunsAChildPerItem(t *testing.T) {
	env := newEnv(t, http.NotFoundHandler())
	env.RegisterWorkflow(FlowWorkflow)
	child := flow.Definition{ID: "child", Name: "child", Nodes: []flow.Node{
		node("start", flow.TypeTrigger, flow.TriggerConfig{Kind: "manual"}),
		node("greet", flow.TypeTransform, flow.TransformConfig{Output: `"hello {{ input.name }}"`}),
	}, Edges: []flow.Edge{edge("start", "greet", "")}}
	def := flow.Definition{ID: "parent", Name: "parent", Nodes: []flow.Node{
		node("start", flow.TypeTrigger, flow.TriggerConfig{Kind: "manual"}),
		node("each", flow.TypeForEach, flow.ForEachConfig{Run: flow.ForEachRunSteps, Items: "input.names", Concurrency: 3}),
		node("call", flow.TypeRunFlow, flow.RunFlowConfig{FlowID: "child", Input: `{"name": "{{ item }}"}`}),
	}, Edges: []flow.Edge{edge("start", "each", ""), edge("each", "call", flow.HandleBody), edge("call", "each", "")}}
	require.Empty(t, def.Validate())
	env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{Flow: def, Input: map[string]any{"names": []any{"a", "b", "c"}},
		Subflows: map[string]flow.Definition{"child": child}})
	require.NoError(t, env.GetWorkflowError())
	st := runState(t, env)
	ids := map[string]bool{}
	for _, item := range st.Items["each"] {
		ids[item.Nodes["call"].ChildWorkflowID] = true
	}
	require.Len(t, ids, 3)
	var out struct {
		Results []map[string]any `json:"results"`
	}
	require.NoError(t, json.Unmarshal(st.Nodes["each"].Output, &out))
	require.Equal(t, "hello b", out.Results[1]["call"].(map[string]any)["greet"])
}

func TestForEachBodyValidation(t *testing.T) {
	def := inlineEach(flow.ForEachConfig{})
	def.Nodes = append(def.Nodes, node("answer", flow.TypeRespond, flow.RespondConfig{Value: `{}`}))
	def.Edges = slices.DeleteFunc(def.Edges, func(e flow.Edge) bool { return e.ID == "label_dear-each" })
	def.Edges = append(def.Edges, edge("label_dear", "answer", ""), edge("answer", "each", ""))
	require.Contains(t, messages(def.Validate()), "answer can't run inside a For each body; put it in a flow and have each run that flow for each item")

	wait := inlineEach(flow.ForEachConfig{})
	wait.Nodes = append(wait.Nodes, node("ack", flow.TypeWaitSignal, flow.WaitSignalConfig{SignalName: "ack"}))
	wait.Edges = slices.DeleteFunc(wait.Edges, func(e flow.Edge) bool { return e.ID == "label_dear-each" })
	wait.Edges = append(wait.Edges, edge("label_dear", "ack", ""), edge("ack", "each", flow.HandleReceived))
	require.Contains(t, messages(wait.Validate()), "ack is inside a For each body: set which events are for which item, for example event.orderId == item.id")

	flowMode := inlineEach(flow.ForEachConfig{})
	for i, n := range flowMode.Nodes {
		if n.ID == "each" {
			flowMode.Nodes[i] = node("each", flow.TypeForEach, flow.ForEachConfig{Items: "input.skus", FlowID: "child", Concurrency: 2})
		}
	}
	require.Contains(t, messages(flowMode.Validate()), "each runs a flow for each item; to run the steps on its body outcome instead, switch it to run steps")

	open := inlineEach(flow.ForEachConfig{})
	open.Edges = slices.DeleteFunc(open.Edges, func(e flow.Edge) bool { return e.Target == "each" && e.Source != "start" })
	require.Contains(t, messages(open.Validate()), "connect the last step of each's body back into each")

	rate := inlineEach(flow.ForEachConfig{Rate: &flow.RateLimit{Count: 0, Per: "hour"}})
	require.NotEmpty(t, rate.Validate())
}

// Faster than one a second, items start a second's worth at a time (timers
// fire about once a second) and keep to the rate on average.
func TestForEachStartRateUnderASecond(t *testing.T) {
	def := flow.Definition{ID: "f", Name: "paced", Nodes: []flow.Node{
		node("start", flow.TypeTrigger, flow.TriggerConfig{Kind: "manual"}),
		node("each", flow.TypeForEach, flow.ForEachConfig{Run: flow.ForEachRunSteps, Items: "input.n", Concurrency: 20,
			Rate: &flow.RateLimit{Count: 5, Per: "second"}}),
		node("stamp", flow.TypeTransform, flow.TransformConfig{Output: `{"i": "{{ index }}"}`}),
	}, Edges: []flow.Edge{edge("start", "each", ""), edge("each", "stamp", flow.HandleBody), edge("stamp", "each", "")}}
	env := newEnv(t, http.NotFoundHandler())
	n := make([]any, 12)
	env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{Flow: def, Input: map[string]any{"n": n}})
	require.NoError(t, env.GetWorkflowError())
	st := runState(t, env)
	// Five at once, then one every 200 ms: the twelfth is due at 1.4 s.
	require.InDelta(t, 1.4, st.EndedAt.Sub(st.StartedAt).Seconds(), 0.05)
}

// approveEach asks a person about each item; each item waits for its own
// decision.
func approveEach() flow.Definition {
	return flow.Definition{ID: "f", Name: "approve each", Nodes: []flow.Node{
		node("start", flow.TypeTrigger, flow.TriggerConfig{Kind: "manual"}),
		node("each", flow.TypeForEach, flow.ForEachConfig{Run: flow.ForEachRunSteps, Items: "input.refunds", Concurrency: 5}),
		node("ok", flow.TypeApproval, flow.ApprovalConfig{Title: "Refund {{ item }}?"}),
		node("paid", flow.TypeTransform, flow.TransformConfig{Output: `{"refund": "{{ item }}"}`}),
		node("refused", flow.TypeTransform, flow.TransformConfig{Output: `{"refused": "{{ item }}"}`}),
	}, Edges: []flow.Edge{
		edge("start", "each", ""), edge("each", "ok", flow.HandleBody),
		edge("ok", "paid", flow.HandleApproved), edge("ok", "refused", flow.HandleRejected),
		edge("paid", "each", ""), edge("refused", "each", ""),
	}}
}

func TestApprovalPerItem(t *testing.T) {
	def := approveEach()
	require.Empty(t, def.Validate())
	env := newEnv(t, http.NotFoundHandler())
	item := func(i int) *int { return &i }
	env.RegisterDelayedCallback(func() {
		st := queryState(t, env)
		require.Equal(t, flow.NodeWaiting, st.Nodes["ok"].Status, "the step sums up three waiting items")
		require.Equal(t, "Refund b?", st.Items["each"][1].Nodes["ok"].Waiting.Title)
		// In a body, a decision names its item.
		env.UpdateWorkflow(flow.UpdateApprove, "no-item", updateCallback(t, func(_ any, err error) { require.Error(t, err) }),
			flow.ApprovalDecision{NodeID: "ok", Approved: true})
		env.UpdateWorkflow(flow.UpdateApprove, "b", updateCallback(t, func(_ any, err error) { require.NoError(t, err) }),
			flow.ApprovalDecision{NodeID: "ok", Item: item(1), Approved: false})
	}, time.Minute)
	env.RegisterDelayedCallback(func() {
		for _, i := range []int{0, 2} {
			env.UpdateWorkflow(flow.UpdateApprove, fmt.Sprint(i), updateCallback(t, func(_ any, err error) { require.NoError(t, err) }),
				flow.ApprovalDecision{NodeID: "ok", Item: item(i), Approved: true})
		}
	}, 2*time.Minute)
	env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{Flow: def, Input: map[string]any{"refunds": []any{"a", "b", "c"}}})
	require.NoError(t, env.GetWorkflowError())
	st := runState(t, env)
	var out struct {
		Results []map[string]any `json:"results"`
	}
	require.NoError(t, json.Unmarshal(st.Nodes["each"].Output, &out))
	require.Contains(t, out.Results[0], "paid")
	require.Equal(t, map[string]any{"refused": "b"}, out.Results[1]["refused"])
	require.Contains(t, out.Results[2], "paid")
	require.InDelta(t, 120, st.EndedAt.Sub(st.StartedAt).Seconds(), 1, "b was decided at one minute, a and c at two")
}

// In a body, Wait for signal takes the events its match picks for the item;
// the rest stay queued for the others.
func TestWaitForSignalPerItem(t *testing.T) {
	def := flow.Definition{ID: "f", Name: "ship each", Nodes: []flow.Node{
		node("start", flow.TypeTrigger, flow.TriggerConfig{Kind: "manual"}),
		node("each", flow.TypeForEach, flow.ForEachConfig{Run: flow.ForEachRunSteps, Items: "input.orders", Concurrency: 5}),
		node("shipped", flow.TypeWaitSignal, flow.WaitSignalConfig{SignalName: "shipped", Match: "event.order == item"}),
		node("note", flow.TypeTransform, flow.TransformConfig{Output: `{"order": "{{ item }}", "carrier": "{{ steps.shipped.payload.carrier }}"}`}),
	}, Edges: []flow.Edge{
		edge("start", "each", ""), edge("each", "shipped", flow.HandleBody), edge("shipped", "note", flow.HandleReceived), edge("note", "each", ""),
	}}
	require.Empty(t, def.Validate())
	env := newEnv(t, http.NotFoundHandler())
	for i, ev := range []map[string]any{
		{"order": "B", "carrier": "ups"}, {"order": "Z", "carrier": "nobody"}, {"order": "A", "carrier": "dhl"}, {"order": "C", "carrier": "fedex"},
	} {
		env.RegisterDelayedCallback(func() { env.SignalWorkflow("shipped", ev) }, time.Duration(i+1)*time.Minute)
	}
	env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{Flow: def, Input: map[string]any{"orders": []any{"A", "B", "C"}}})
	require.NoError(t, env.GetWorkflowError())
	st := runState(t, env)
	var out struct {
		Results []map[string]any `json:"results"`
	}
	require.NoError(t, json.Unmarshal(st.Nodes["each"].Output, &out))
	for i, want := range []string{"dhl", "ups", "fedex"} {
		require.Equal(t, want, out.Results[i]["note"].(map[string]any)["carrier"])
	}
	require.Equal(t, 1, st.Events["shipped"].Pending, "the event for Z is still queued")
}

// A failed run undoes every item's completed requests, newest first.
func TestUndoPerItemWhenTheRunFails(t *testing.T) {
	var calls []string
	var mu sync.Mutex
	env := newEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls = append(calls, r.Method+" "+r.URL.Path)
		mu.Unlock()
		if r.URL.Path == "/charge" {
			w.WriteHeader(http.StatusPaymentRequired)
		}
		_, _ = w.Write([]byte(`{"id": "x"}`))
	}))
	def := flow.Definition{ID: "f", Name: "reserve all", Nodes: []flow.Node{
		node("start", flow.TypeTrigger, flow.TriggerConfig{Kind: "manual"}),
		node("each", flow.TypeForEach, flow.ForEachConfig{Run: flow.ForEachRunSteps, Items: "input.seats", Concurrency: 1}),
		node("hold", flow.TypeHTTP, flow.HTTPConfig{Method: "POST", URL: baseURL + "/hold/{{ item }}",
			Undo: &flow.HTTPRequestSpec{Method: "DELETE", URL: baseURL + "/hold/{{ item }}"}}),
		node("charge", flow.TypeHTTP, flow.HTTPConfig{Method: "POST", URL: baseURL + "/charge"}),
	}, Edges: []flow.Edge{
		edge("start", "each", ""), edge("each", "hold", flow.HandleBody), edge("hold", "each", ""), edge("each", "charge", ""),
	}}
	require.Empty(t, def.Validate())
	env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{Flow: def, Input: map[string]any{"seats": []any{"1A", "1B", "1C"}}})
	require.Error(t, env.GetWorkflowError())
	require.Equal(t, []string{
		"POST /hold/1A", "POST /hold/1B", "POST /hold/1C", "POST /charge",
		"DELETE /hold/1C", "DELETE /hold/1B", "DELETE /hold/1A",
	}, calls)
}

// A step paused for a fix in a body waits per item; a fix names its item.
func TestFixPerItem(t *testing.T) {
	env := newEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "bad") {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"ok": true}`))
	}))
	def := flow.Definition{ID: "f", Name: "sync", Nodes: []flow.Node{
		node("start", flow.TypeTrigger, flow.TriggerConfig{Kind: "manual"}),
		node("each", flow.TypeForEach, flow.ForEachConfig{Run: flow.ForEachRunSteps, Items: "input.ids", Concurrency: 5}),
		node("push", flow.TypeHTTP, flow.HTTPConfig{Method: "POST", URL: baseURL + "/push/{{ item }}", PauseForFix: true,
			Activity: flow.ActivityOptions{Retry: flow.RetryConfig{MaxAttempts: 1}}}),
	}, Edges: []flow.Edge{edge("start", "each", ""), edge("each", "push", flow.HandleBody), edge("push", "each", "")}}
	require.Empty(t, def.Validate())
	one := 1
	env.RegisterDelayedCallback(func() {
		st := queryState(t, env)
		require.Equal(t, "fix", st.Items["each"][1].Nodes["push"].Waiting.Kind)
		require.Equal(t, flow.NodeCompleted, st.Items["each"][0].Status)
		env.UpdateWorkflow(flow.UpdateFix, "fix", updateCallback(t, func(_ any, err error) { require.NoError(t, err) }),
			flow.FixDecision{NodeID: "push", Item: &one, Action: "retry", URL: baseURL + "/push/good"})
	}, time.Minute)
	env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{Flow: def, Input: map[string]any{"ids": []any{"a", "bad", "c"}}})
	require.NoError(t, env.GetWorkflowError())
	st := runState(t, env)
	require.Equal(t, "3 done", st.Nodes["push"].Progress)
}

// A request on a shared limit runs on the limit's task queue; one on a limit
// the deployment doesn't have fails at once instead of waiting for ever.
func TestSharedLimit(t *testing.T) {
	limited := func() flow.Definition {
		return flow.Definition{ID: "f", Name: "limited", Nodes: []flow.Node{
			node("start", flow.TypeTrigger, flow.TriggerConfig{Kind: "manual"}),
			node("call", flow.TypeHTTP, flow.HTTPConfig{Method: "GET", URL: baseURL + "/x", RateLimit: "crm"}),
		}, Edges: []flow.Edge{edge("start", "call", "")}}
	}
	var queue string
	env := newEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{}`)) }))
	def := limited()
	require.Equal(t, []string{"crm"}, def.SharedLimits())
	env.SetOnActivityStartedListener(func(info *activity.Info, _ context.Context, _ converter.EncodedValues) {
		queue = info.TaskQueue
	})
	env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{Flow: def, SharedLimits: []string{"crm"}})
	require.NoError(t, env.GetWorkflowError())
	require.Equal(t, flow.LimitTaskQueue("default-test-taskqueue", "crm"), queue)

	env = newEnv(t, http.NotFoundHandler())
	env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{Flow: limited()})
	require.ErrorContains(t, env.GetWorkflowError(), `rate limit "crm" isn't configured`)
}
