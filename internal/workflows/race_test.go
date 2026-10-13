package workflows

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/flow"
)

// carriers answers quote requests: /quote/budget is down (a 400, so it is
// not retried and fails at once); every other carrier quotes.
func carriers() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/budget") {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"price": 12}`))
	})
}

// quoteRace fans out to three branches and races them: budget fails at once,
// swift quotes after 10 seconds, and slow quotes after 5 minutes.
func quoteRace(wins string, withFallback bool, failing ...string) flow.Definition {
	call := func(carrier string) flow.HTTPConfig {
		return flow.HTTPConfig{Method: "GET", URL: baseURL + "/quote/" + carrier}
	}
	def := flow.Definition{ID: "f", Name: "quotes", Nodes: []flow.Node{
		node("start", flow.TypeTrigger, flow.TriggerConfig{Kind: "manual"}),
		node("budget", flow.TypeHTTP, call("budget")),
		node("swift_wait", flow.TypeDelay, flow.DelayConfig{Mode: "duration", Amount: 10, Unit: "seconds"}),
		node("swift", flow.TypeHTTP, call("swift")),
		node("slow_wait", flow.TypeDelay, flow.DelayConfig{Mode: "duration", Amount: 5, Unit: "minutes"}),
		node("slow", flow.TypeHTTP, call("slow")),
		node("race", flow.TypeFirstOf, flow.FirstOfConfig{Wins: wins}),
		node("book", flow.TypeTransform, flow.TransformConfig{Output: `{"carrier":"{{ steps.race.winner }}"}`}),
	}, Edges: []flow.Edge{
		edge("start", "budget", ""), edge("start", "swift_wait", ""), edge("start", "slow_wait", ""),
		edge("swift_wait", "swift", ""), edge("slow_wait", "slow", ""),
		edge("budget", "race", ""), edge("swift", "race", ""), edge("slow", "race", ""),
		edge("race", "book", ""),
	}}
	for _, carrier := range failing {
		for i, n := range def.Nodes {
			if n.ID == carrier {
				def.Nodes[i] = node(carrier, flow.TypeHTTP, call("budget"))
			}
		}
	}
	if withFallback {
		def.Nodes = append(def.Nodes, node("fallback", flow.TypeTransform, flow.TransformConfig{Output: `{"why":"{{ steps.race.error }}"}`}))
		def.Edges = append(def.Edges, edge("race", "fallback", flow.HandleError))
	}
	return def
}

func TestFirstToSucceedIgnoresAFailedBranch(t *testing.T) {
	env := newEnv(t, carriers())
	env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{Flow: quoteRace(flow.FirstOfSucceeds, false)})
	require.NoError(t, env.GetWorkflowError())
	st := runState(t, env)

	require.Equal(t, flow.NodeFailed, st.Nodes["budget"].Status, "the failed branch stays visible")
	require.JSONEq(t, `{"carrier":"swift"}`, string(st.Nodes["book"].Output))
	require.Equal(t, flow.NodeSkipped, st.Nodes["slow_wait"].Status, "the slow branch lost and was cancelled")
	require.Equal(t, "lost the race to swift", st.Nodes["slow_wait"].Error)
	require.InDelta(t, 10, st.EndedAt.Sub(st.StartedAt).Seconds(), 1, "the run doesn't wait for the slow carrier")
}

func TestFirstToArriveFailsOnAFailedBranch(t *testing.T) {
	env := newEnv(t, carriers())
	env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{Flow: quoteRace(flow.FirstOfArrives, false)})
	require.Error(t, env.GetWorkflowError(), "without first-to-succeed, a failed branch fails the run")
}

func TestFirstToSucceedTakesTheErrorOutcomeWhenEveryBranchFails(t *testing.T) {
	env := newEnv(t, carriers())
	env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{Flow: quoteRace(flow.FirstOfSucceeds, true, "swift", "slow")})
	require.NoError(t, env.GetWorkflowError())
	st := runState(t, env)

	require.Equal(t, flow.HandleError, st.Nodes["race"].Handle)
	require.Contains(t, string(st.Nodes["fallback"].Output), "every branch failed")
	require.Equal(t, flow.NodeSkipped, st.Nodes["book"].Status)
	// It waited for the last branch (five minutes) before giving up.
	require.InDelta(t, 300, st.EndedAt.Sub(st.StartedAt).Seconds(), 1)
}

func TestFirstToSucceedFailsTheRunWithoutAnErrorOutcome(t *testing.T) {
	env := newEnv(t, carriers())
	env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{Flow: quoteRace(flow.FirstOfSucceeds, false, "swift", "slow")})
	err := env.GetWorkflowError()
	require.Error(t, err)
	require.Contains(t, err.Error(), "every branch failed")
}

func TestErrorOutcomeNeedsFirstToSucceed(t *testing.T) {
	def := quoteRace(flow.FirstOfArrives, true)
	problems := def.Validate()
	require.Len(t, problems, 1)
	require.Equal(t, "race", problems[0].NodeID)
}

// quoteChild waits input.seconds, then asks one carrier for a quote.
func quoteChild() flow.Definition {
	return flow.Definition{ID: "quote", Name: "quote", Nodes: []flow.Node{
		node("start", flow.TypeTrigger, flow.TriggerConfig{Kind: "manual"}),
		node("think", flow.TypeDelay, flow.DelayConfig{Mode: "expression", Expression: "input.seconds"}),
		node("ask", flow.TypeHTTP, flow.HTTPConfig{Method: "GET", URL: baseURL + "/quote/{{ input.carrier }}"}),
	}, Edges: []flow.Edge{edge("start", "think", ""), edge("think", "ask", "")}}
}

func forEachQuotes(finishWhen string, count int) flow.Definition {
	return flow.Definition{ID: "parent", Name: "parent", Nodes: []flow.Node{
		node("start", flow.TypeTrigger, flow.TriggerConfig{Kind: "manual"}),
		node("each", flow.TypeForEach, flow.ForEachConfig{
			Items: "input.carriers", FlowID: "quote", Concurrency: 5, FinishWhen: finishWhen, Count: count,
			Input: `{"carrier": "{{ item.name }}", "seconds": "{{ item.seconds }}"}`,
		}),
	}, Edges: []flow.Edge{edge("start", "each", "")}}
}

var carrierItems = []any{
	map[string]any{"name": "budget", "seconds": 1},
	map[string]any{"name": "slow", "seconds": 300},
	map[string]any{"name": "swift", "seconds": 10},
}

func TestForEachFinishesOnTheFirstChildToSucceed(t *testing.T) {
	env := newEnv(t, carriers())
	env.RegisterWorkflow(FlowWorkflow)
	env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{
		Flow: forEachQuotes(flow.ForEachFirst, 0), Input: map[string]any{"carriers": carrierItems},
		Subflows: map[string]flow.Definition{"quote": quoteChild()},
	})
	require.NoError(t, env.GetWorkflowError())
	st := runState(t, env)

	var out struct {
		Winner struct {
			Index int            `json:"index"`
			Item  map[string]any `json:"item"`
		} `json:"winner"`
		Failed int `json:"failed"`
	}
	require.NoError(t, json.Unmarshal(st.Nodes["each"].Output, &out))
	require.Equal(t, 2, out.Winner.Index)
	require.Equal(t, "swift", out.Winner.Item["name"])
	require.Equal(t, 1, out.Failed, "budget failed and was tolerated")
	require.Equal(t, "1 of 1 needed · 1 failed", st.Nodes["each"].Progress)
	require.InDelta(t, 10, st.EndedAt.Sub(st.StartedAt).Seconds(), 1, "the slow child was cancelled, not waited for")
}

func TestForEachQuorumFailsWhenTooManyChildrenFail(t *testing.T) {
	env := newEnv(t, carriers())
	env.RegisterWorkflow(FlowWorkflow)
	items := append([]any{map[string]any{"name": "budget", "seconds": 2}}, carrierItems...)
	env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{
		Flow: forEachQuotes(flow.ForEachCount, 3), Input: map[string]any{"carriers": items},
		Subflows: map[string]flow.Definition{"quote": quoteChild()},
	})
	err := env.GetWorkflowError()
	require.Error(t, err)
	// Decided when the second budget child failed: with two of four failed,
	// three can't succeed, so it doesn't wait for swift or slow.
	require.Contains(t, err.Error(), "0 of the 3 needed succeeded")
}

// The losing branch's HTTP request is aborted, not left running: the
// cancellation reaches the Activity, which cancels its request.
func TestLosingRequestIsAborted(t *testing.T) {
	// The SDK sends heartbeats at most every 80% of the heartbeat timeout,
	// and the cancellation rides on the response; shorten it to keep the
	// test quick.
	saved := HeartbeatTimeout
	HeartbeatTimeout = time.Second
	t.Cleanup(func() { HeartbeatTimeout = saved })
	aborted := make(chan struct{})
	env := newEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/slow") {
			<-r.Context().Done()
			close(aborted)
			return
		}
		_, _ = w.Write([]byte(`{"price": 12}`))
	}))
	def := flow.Definition{ID: "f", Name: "abort", Nodes: []flow.Node{
		node("start", flow.TypeTrigger, flow.TriggerConfig{Kind: "manual"}),
		node("fast", flow.TypeHTTP, flow.HTTPConfig{Method: "GET", URL: baseURL + "/fast"}),
		node("slow", flow.TypeHTTP, flow.HTTPConfig{Method: "GET", URL: baseURL + "/slow"}),
		node("race", flow.TypeFirstOf, flow.FirstOfConfig{}),
	}, Edges: []flow.Edge{
		edge("start", "fast", ""), edge("start", "slow", ""),
		edge("fast", "race", ""), edge("slow", "race", ""),
	}}
	env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{Flow: def})
	require.NoError(t, env.GetWorkflowError())
	require.Equal(t, flow.NodeSkipped, runState(t, env).Nodes["slow"].Status)
	select {
	case <-aborted:
	case <-time.After(5 * time.Second):
		t.Fatal("the losing request was never aborted")
	}
}
