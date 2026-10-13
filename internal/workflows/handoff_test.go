package workflows

import (
	"encoding/json"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/flow"
)

// continuedAsNew asserts the run continued as new and returns the next run's
// input, so a test can run it in a fresh environment.
func continuedAsNew(t *testing.T, env *testsuite.TestWorkflowEnvironment) flow.RunInput {
	t.Helper()
	var can *workflow.ContinueAsNewError
	require.True(t, errors.As(env.GetWorkflowError(), &can), "expected Continue-As-New, got %v", env.GetWorkflowError())
	var next flow.RunInput
	require.NoError(t, converter.GetDefaultDataConverter().FromPayloads(can.Input, &next))
	require.NotNil(t, next.Resume)
	return next
}

func suggestContinueAsNewAt(env *testsuite.TestWorkflowEnvironment, at time.Duration) {
	env.RegisterDelayedCallback(func() { env.SetContinueAsNewSuggested(true) }, at)
}

// entity is a customer that lives until its account is closed.
func entity() flow.Definition {
	return flow.Definition{ID: "f", Name: "entity", Settings: flow.FlowSettings{
		KeyedBy:   "input.customerId",
		Variables: []flow.VariableDef{{Name: "points", Initial: `0`}},
	}, Nodes: []flow.Node{
		node("start", flow.TypeTrigger, flow.TriggerConfig{Kind: "manual"}),
		node("welcome", flow.TypeTransform, flow.TransformConfig{Output: `{"welcomed": true}`}),
		node("purchase", flow.TypeOnSignal, flow.OnSignalConfig{SignalName: "purchase"}),
		node("add", flow.TypeSetVariable, flow.SetVariableConfig{Assignments: []flow.Assignment{{Name: "points", Value: "{{ vars.points + steps.purchase.amount }}"}}}),
		node("close", flow.TypeOnSignal, flow.OnSignalConfig{SignalName: "close_account"}),
		node("end", flow.TypeEnd, flow.EndConfig{}),
	}, Edges: []flow.Edge{edge("start", "welcome", ""), edge("purchase", "add", ""), edge("close", "end", "")}}
}

func TestEntityLivesUntilAnEndRunStep(t *testing.T) {
	env := newEnv(t, http.NotFoundHandler())
	def := entity()
	require.Empty(t, def.Validate())
	env.RegisterDelayedCallback(func() { env.SignalWorkflow("purchase", map[string]any{"amount": 50}) }, 24*time.Hour)
	// A year of silence, then the account closes.
	env.RegisterDelayedCallback(func() { env.SignalWorkflow("close_account", nil) }, 400*24*time.Hour)
	env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{Flow: entity(), Key: "C-42"})
	require.NoError(t, env.GetWorkflowError())
	st := runState(t, env)
	require.EqualValues(t, 50, st.Vars["points"], "state survived the idle year")
	require.GreaterOrEqual(t, st.EndedAt.Sub(st.StartedAt), 400*24*time.Hour)
	require.Equal(t, flow.NodeCompleted, st.Nodes["end"].Status)
}

// Continue-As-New is Temporal's call: hundreds of events don't trigger it,
// the suggestion does.
func TestContinuesAsNewOnlyWhenTemporalSuggests(t *testing.T) {
	env := newEnv(t, http.NotFoundHandler())
	for i := range 500 {
		env.RegisterDelayedCallback(func() { env.SignalWorkflow("purchase", map[string]any{"amount": 1}) }, time.Duration(i+1)*time.Second)
	}
	env.RegisterDelayedCallback(func() { env.SignalWorkflow("close_account", nil) }, time.Hour)
	env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{Flow: entity(), Key: "C-42"})
	require.NoError(t, env.GetWorkflowError(), "500 events and no suggestion: no Continue-As-New")
	require.EqualValues(t, 500, runState(t, env).Vars["points"])

	env = newEnv(t, http.NotFoundHandler())
	env.RegisterDelayedCallback(func() { env.SignalWorkflow("purchase", map[string]any{"amount": 7}) }, time.Minute)
	suggestContinueAsNewAt(env, 2*time.Minute)
	env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{Flow: entity(), Key: "C-42"})
	next := continuedAsNew(t, env)
	require.Equal(t, 1, next.Resume.Generation)

	// The next generation keeps the entity's state and doesn't rerun the
	// main path.
	env = newEnv(t, http.NotFoundHandler())
	env.RegisterDelayedCallback(func() { env.SignalWorkflow("purchase", map[string]any{"amount": 3}) }, time.Minute)
	env.RegisterDelayedCallback(func() { env.SignalWorkflow("close_account", nil) }, 2*time.Minute)
	env.ExecuteWorkflow(FlowWorkflow, next)
	require.NoError(t, env.GetWorkflowError())
	st := runState(t, env)
	require.EqualValues(t, 10, st.Vars["points"])
	require.Equal(t, 1, st.Generation)
	require.Equal(t, flow.NodeCompleted, st.Nodes["welcome"].Status)
	require.Equal(t, 1, st.Nodes["start"].Invocations, "the main path ran once, in the first generation")
}

// A loop on the main path hands off mid-pause and the next run carries on
// with the same pass count.
func TestLoopContinuesAsNewPartWay(t *testing.T) {
	var calls atomic.Int32
	env := newEnv(t, readyAfter(5, &calls))
	suggestContinueAsNewAt(env, 25*time.Second)
	env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{Flow: pollFlow(0, false)})
	next := continuedAsNew(t, env)
	require.EqualValues(t, 3, calls.Load(), "three passes before the hand-off")

	env = newEnv(t, readyAfter(5, &calls))
	env.ExecuteWorkflow(FlowWorkflow, next)
	require.NoError(t, env.GetWorkflowError())
	st := runState(t, env)
	require.EqualValues(t, 5, calls.Load())
	require.JSONEq(t, `{"passes": 5}`, string(st.Nodes["ready"].Output))
	require.Equal(t, flow.NodeCompleted, st.Nodes["start"].Status)
}

// A For each over many items hands off between children: the ones running
// finish, the rest start in the next run.
func TestForEachContinuesAsNewPartWay(t *testing.T) {
	env := newEnv(t, http.NotFoundHandler())
	env.RegisterWorkflow(FlowWorkflow)
	child := flow.Definition{ID: "child", Name: "child", Nodes: []flow.Node{
		node("start", flow.TypeTrigger, flow.TriggerConfig{Kind: "manual"}),
		node("pause", flow.TypeDelay, flow.DelayConfig{Mode: "duration", Amount: 1, Unit: "minutes"}),
		node("greet", flow.TypeTransform, flow.TransformConfig{Output: `"hello {{ input.name }}"`}),
	}, Edges: []flow.Edge{edge("start", "pause", ""), edge("pause", "greet", "")}}
	parent := flow.Definition{ID: "parent", Name: "parent", Nodes: []flow.Node{
		node("start", flow.TypeTrigger, flow.TriggerConfig{Kind: "manual"}),
		node("each", flow.TypeForEach, flow.ForEachConfig{Items: "input.names", FlowID: "child", Concurrency: 2, Input: `{"name": "{{ item }}"}`}),
		node("after", flow.TypeTransform, flow.TransformConfig{Output: `{"n": "{{ steps.each.count }}"}`}),
	}, Edges: []flow.Edge{edge("start", "each", ""), edge("each", "after", "")}}
	in := flow.RunInput{Flow: parent, Input: map[string]any{"names": []any{"a", "b", "c", "d", "e", "f"}}, Subflows: map[string]flow.Definition{"child": child}}

	suggestContinueAsNewAt(env, 90*time.Second)
	env.ExecuteWorkflow(FlowWorkflow, in)
	next := continuedAsNew(t, env)

	env = newEnv(t, http.NotFoundHandler())
	env.RegisterWorkflow(FlowWorkflow)
	env.ExecuteWorkflow(FlowWorkflow, next)
	require.NoError(t, env.GetWorkflowError())
	st := runState(t, env)
	var out struct {
		Count     int              `json:"count"`
		Succeeded int              `json:"succeeded"`
		Results   []map[string]any `json:"results"`
	}
	require.NoError(t, jsonUnmarshal(st.Nodes["each"].Output, &out))
	require.Equal(t, 6, out.Count)
	require.Equal(t, 6, out.Succeeded, "four before the hand-off (two finishing during it), two after")
	require.Equal(t, "hello a", out.Results[0]["greet"], "results from the first run carried over")
	require.Equal(t, "hello f", out.Results[5]["greet"])
	// The second run only had items e and f: two at a time, one minute.
	require.InDelta(t, 60, st.EndedAt.Sub(st.StartedAt).Seconds(), 1)
}

// A person-wait is interrupted by the hand-off and resumed in the next run;
// a decision arrives there.
func TestApprovalResumesAfterContinueAsNew(t *testing.T) {
	def := flow.Definition{ID: "f", Name: "approve", Nodes: []flow.Node{
		node("start", flow.TypeTrigger, flow.TriggerConfig{Kind: "manual"}),
		node("ok", flow.TypeApproval, flow.ApprovalConfig{Title: "Ship it?", TimeoutSeconds: 3600}),
		node("yes", flow.TypeTransform, flow.TransformConfig{Output: `{"shipped": true}`}),
	}, Edges: []flow.Edge{edge("start", "ok", ""), edge("ok", "yes", flow.HandleApproved)}}
	env := newEnv(t, http.NotFoundHandler())
	suggestContinueAsNewAt(env, 10*time.Minute)
	env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{Flow: def})
	next := continuedAsNew(t, env)
	require.Equal(t, flow.NodePending, next.Resume.Nodes["ok"].Status)

	env = newEnv(t, http.NotFoundHandler())
	env.RegisterDelayedCallback(func() {
		env.UpdateWorkflow(flow.UpdateApprove, "a1", updateCallback(t, func(_ any, err error) { require.NoError(t, err) }),
			flow.ApprovalDecision{NodeID: "ok", Approved: true})
	}, time.Minute)
	env.ExecuteWorkflow(FlowWorkflow, next)
	require.NoError(t, env.GetWorkflowError())
	st := runState(t, env)
	require.Equal(t, flow.HandleApproved, st.Nodes["ok"].Handle)
	require.JSONEq(t, `{"shipped": true}`, string(st.Nodes["yes"].Output))
}

// End run from a lane stops the main path and completes the run.
func TestEndRunStopsTheMainPath(t *testing.T) {
	def := flow.Definition{ID: "f", Name: "end", Nodes: []flow.Node{
		node("start", flow.TypeTrigger, flow.TriggerConfig{Kind: "manual"}),
		node("wait", flow.TypeWaitSignal, flow.WaitSignalConfig{SignalName: "never"}),
		node("after", flow.TypeTransform, flow.TransformConfig{Output: `{}`}),
		node("cancel", flow.TypeOnSignal, flow.OnSignalConfig{SignalName: "cancel"}),
		node("end", flow.TypeEnd, flow.EndConfig{}),
	}, Edges: []flow.Edge{edge("start", "wait", ""), edge("wait", "after", flow.HandleReceived), edge("cancel", "end", "")}}
	require.Empty(t, def.Validate())
	env := newEnv(t, http.NotFoundHandler())
	env.RegisterDelayedCallback(func() { env.SignalWorkflow("cancel", nil) }, time.Hour)
	env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{Flow: def})
	require.NoError(t, env.GetWorkflowError())
	st := runState(t, env)
	require.Equal(t, flow.RunCompleted, st.Status)
	require.Equal(t, "the run was ended", st.Nodes["wait"].Error)
	require.Equal(t, flow.NodeSkipped, st.Nodes["after"].Status)
}

// Runs continued as new by earlier code carry no interpreter state: their
// main path had finished, and the run keeps serving its key.
func TestResumesFromAnEarlierBuildsState(t *testing.T) {
	env := newEnv(t, http.NotFoundHandler())
	env.RegisterDelayedCallback(func() { env.SignalWorkflow("purchase", map[string]any{"amount": 5}) }, time.Minute)
	env.RegisterDelayedCallback(func() { env.SignalWorkflow("close_account", nil) }, 2*time.Minute)
	env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{Flow: entity(), Key: "C-42",
		Resume: &flow.ResumeState{Vars: map[string]any{"points": 100}, Generation: 3}})
	require.NoError(t, env.GetWorkflowError())
	st := runState(t, env)
	require.EqualValues(t, 105, st.Vars["points"])
	require.Equal(t, "earlier run", st.Nodes["welcome"].Progress)
}

func jsonUnmarshal(raw []byte, v any) error { return json.Unmarshal(raw, v) }

// Events still queued for a lane when the hand-off starts are carried over,
// not handled twice or lost, and don't hold the hand-off up: the lane's
// running invocation finishes, the rest wait for the next run.
func TestQueuedLaneEventsCarryAcrossContinueAsNew(t *testing.T) {
	slow := entity()
	slow.Nodes = append(slow.Nodes, node("think", flow.TypeDelay, flow.DelayConfig{Mode: "duration", Amount: 1, Unit: "minutes"}))
	slow.Edges = []flow.Edge{edge("start", "welcome", ""), edge("purchase", "think", ""), edge("think", "add", ""), edge("close", "end", "")}
	require.Empty(t, slow.Validate())

	env := newEnv(t, http.NotFoundHandler())
	env.RegisterDelayedCallback(func() {
		for range 5 {
			env.SignalWorkflow("purchase", map[string]any{"amount": 1})
		}
	}, time.Second)
	suggestContinueAsNewAt(env, 30*time.Second)
	env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{Flow: slow, Key: "C-42"})
	next := continuedAsNew(t, env)
	require.EqualValues(t, 1, next.Resume.Vars["points"], "the running invocation finished before the hand-off")

	env = newEnv(t, http.NotFoundHandler())
	env.RegisterDelayedCallback(func() { env.SignalWorkflow("close_account", nil) }, time.Hour)
	env.ExecuteWorkflow(FlowWorkflow, next)
	require.NoError(t, env.GetWorkflowError())
	require.EqualValues(t, 5, runState(t, env).Vars["points"], "the four queued purchases ran in the next run, each once")
}
