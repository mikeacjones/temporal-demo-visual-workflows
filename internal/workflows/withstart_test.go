package workflows

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/flow"
)

// initFlow has an On start lane that takes a minute to set the cart up, a
// main path that reads what it set, and a lane per message kind.
func initFlow() flow.Definition {
	return flow.Definition{ID: "f", Name: "cart", Settings: flow.FlowSettings{
		KeyedBy: "input.cartId",
		Variables: []flow.VariableDef{
			{Name: "currency", Initial: `""`},
			{Name: "items", Initial: `0`},
		},
	}, Nodes: []flow.Node{
		node("start", flow.TypeTrigger, flow.TriggerConfig{Kind: "manual"}),
		node("seen", flow.TypeTransform, flow.TransformConfig{Output: `"{{ vars.currency }}"`}),
		node("hold", flow.TypeDelay, flow.DelayConfig{Mode: "duration", Amount: 5, Unit: "minutes"}),
		node("init", flow.TypeOnStart, flow.OnStartConfig{}),
		node("load", flow.TypeDelay, flow.DelayConfig{Mode: "duration", Amount: 1, Unit: "minutes"}),
		node("setup", flow.TypeSetVariable, flow.SetVariableConfig{Assignments: []flow.Assignment{
			{Name: "currency", Value: "EUR"}, {Name: "items", Value: "{{ 10 }}"},
		}}),
		node("add_item", flow.TypeOnSignal, flow.OnSignalConfig{SignalName: "add_item"}),
		node("count", flow.TypeSetVariable, flow.SetVariableConfig{Assignments: []flow.Assignment{{Name: "items", Value: "{{ vars.items + 1 }}"}}}),
		node("price", flow.TypeOnUpdate, flow.OnUpdateConfig{UpdateName: "price"}),
		node("quote", flow.TypeRespond, flow.RespondConfig{Value: `{"currency": "{{ vars.currency }}"}`}),
	}, Edges: []flow.Edge{
		edge("start", "seen", ""), edge("seen", "hold", ""),
		edge("init", "load", ""), edge("load", "setup", ""),
		edge("add_item", "count", ""), edge("price", "quote", ""),
	}}
}

// What Signal- and Update-With-Start deliver arrives with the start, before
// the run has done anything. With an On start lane, the messages wait for it,
// and so does the main path.
func TestMessagesSentWithStartWaitForTheInitLane(t *testing.T) {
	def := initFlow()
	require.Empty(t, def.Validate())
	env := newEnv(t, http.NotFoundHandler())

	var answered any
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("add_item", map[string]any{})
		env.UpdateWorkflow("price", "u1", updateCallback(t, func(v any, err error) {
			require.NoError(t, err)
			answered = v
		}), map[string]any{})
	}, 0)
	env.RegisterDelayedCallback(func() {
		st := queryState(t, env)
		require.Equal(t, flow.NodeWaiting, st.Nodes["load"].Status)
		require.Equal(t, flow.NodePending, st.Nodes["seen"].Status, "the main path waits for init")
		require.Equal(t, flow.NodePending, st.Nodes["count"].Status, "the lane waits for init")
		require.Nil(t, answered, "the update waits for init")
	}, 30*time.Second)

	env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{Flow: def, Key: "cart-1", Input: map[string]any{"cartId": "cart-1"}})
	require.NoError(t, env.GetWorkflowError())
	st := runState(t, env)
	require.Equal(t, map[string]any{"currency": "EUR"}, answered)
	require.JSONEq(t, `"EUR"`, string(st.Nodes["seen"].Output))
	// Init set 10; the early event counted on top of it, not before it.
	require.EqualValues(t, 11, st.Vars["items"])
	require.InDelta(t, 6*60, st.EndedAt.Sub(st.StartedAt).Seconds(), 1)
}

func TestFailedInitLaneFailsTheRunAndRejectsMessages(t *testing.T) {
	def := initFlow()
	env := newEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusInternalServerError) }))
	def.Nodes[4] = node("load", flow.TypeHTTP, flow.HTTPConfig{Method: "GET", URL: baseURL + "/cart", Activity: flow.ActivityOptions{Retry: flow.RetryConfig{MaxAttempts: 1}}})
	require.Empty(t, def.Validate())

	var updateErr error
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("add_item", map[string]any{})
		env.UpdateWorkflow("price", "u1", updateCallback(t, func(_ any, err error) { updateErr = err }), map[string]any{})
	}, 0)

	env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{Flow: def, Key: "cart-1"})
	require.Error(t, env.GetWorkflowError())
	require.ErrorContains(t, updateErr, "On start lane failed")
	st := queryState(t, env)
	require.Equal(t, flow.NodeFailed, st.Nodes["load"].Status)
	require.Equal(t, flow.NodePending, st.Nodes["seen"].Status, "the main path never ran")
	require.Equal(t, flow.NodePending, st.Nodes["count"].Status, "the queued event was dropped")
	require.EqualValues(t, 0, st.Vars["items"])
}

func TestInitLaneValidation(t *testing.T) {
	def := initFlow()
	def.Nodes = append(def.Nodes, node("init2", flow.TypeOnStart, flow.OnStartConfig{}))
	require.Contains(t, messages(def.Validate()), "flow can have at most one On start step (found 2)")

	def = initFlow()
	def.Nodes = append(def.Nodes, node("reply", flow.TypeRespond, flow.RespondConfig{Value: `{}`}))
	def.Edges = append(def.Edges, edge("setup", "reply", ""))
	require.Contains(t, messages(def.Validate()), "Respond answers a caller, and On start has none; use it on the main path or in an On update lane")
}

func messages(problems []flow.Problem) []string {
	var out []string
	for _, p := range problems {
		out = append(out, p.Message)
	}
	return out
}

// A child the run doesn't wait for is abandoned: the step finishes as soon as
// the child has started, and the parent can complete while it runs on.
func TestRunFlowWithoutWaitingAbandonsTheChild(t *testing.T) {
	env := newEnv(t, http.NotFoundHandler())
	env.RegisterWorkflow(FlowWorkflow)
	child := flow.Definition{ID: "child", Name: "child", Nodes: []flow.Node{
		node("start", flow.TypeTrigger, flow.TriggerConfig{Kind: "manual"}),
		node("pause", flow.TypeDelay, flow.DelayConfig{Mode: "duration", Amount: 10, Unit: "minutes"}),
	}, Edges: []flow.Edge{edge("start", "pause", "")}}
	parent := flow.Definition{ID: "parent", Name: "parent", Nodes: []flow.Node{
		node("start", flow.TypeTrigger, flow.TriggerConfig{Kind: "manual"}),
		node("kick_off", flow.TypeRunFlow, flow.RunFlowConfig{FlowID: "child", ParentClosePolicy: "abandon", Wait: flow.RunFlowWaitStart}),
		node("after", flow.TypeTransform, flow.TransformConfig{Output: `"{{ steps.kick_off.workflowId }}"`}),
	}, Edges: []flow.Edge{edge("start", "kick_off", ""), edge("kick_off", "after", "")}}
	require.Empty(t, parent.Validate())

	env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{Flow: parent, Subflows: map[string]flow.Definition{"child": child}})
	require.NoError(t, env.GetWorkflowError())
	st := runState(t, env)
	var out map[string]any
	require.NoError(t, json.Unmarshal(st.Nodes["kick_off"].Output, &out))
	require.Contains(t, out["workflowId"], "-child-kick_off")
	require.NotEmpty(t, out["runId"])
	require.JSONEq(t, `"`+out["workflowId"].(string)+`"`, string(st.Nodes["after"].Output))
	require.Less(t, st.EndedAt.Sub(st.StartedAt), time.Minute, "the parent didn't wait for the child's 10 minutes")
}

// Every run of a key shares its Workflow ID, and a child abandoned by one run
// may outlive it, so a keyed run's child IDs carry its run chain too.
func TestKeyedRunsNameChildrenPerRunChain(t *testing.T) {
	env := newEnv(t, http.NotFoundHandler())
	env.RegisterWorkflow(FlowWorkflow)
	child := flow.Definition{ID: "child", Name: "child", Nodes: []flow.Node{node("start", flow.TypeTrigger, flow.TriggerConfig{Kind: "manual"})}}
	parent := flow.Definition{ID: "parent", Name: "parent", Settings: flow.FlowSettings{KeyedBy: "input.id"}, Nodes: []flow.Node{
		node("start", flow.TypeTrigger, flow.TriggerConfig{Kind: "manual"}),
		node("kick_off", flow.TypeRunFlow, flow.RunFlowConfig{FlowID: "child", ParentClosePolicy: "abandon", Wait: flow.RunFlowWaitStart}),
	}, Edges: []flow.Edge{edge("start", "kick_off", "")}}

	env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{Flow: parent, Key: "k1", Subflows: map[string]flow.Definition{"child": child}})
	require.NoError(t, env.GetWorkflowError())
	var out map[string]any
	require.NoError(t, json.Unmarshal(runState(t, env).Nodes["kick_off"].Output, &out))
	require.Regexp(t, `-child-kick_off-.{8}$`, out["workflowId"])
}

func TestRunFlowWithoutWaitingMustAbandon(t *testing.T) {
	def := flow.Definition{ID: "parent", Name: "parent", Nodes: []flow.Node{
		node("start", flow.TypeTrigger, flow.TriggerConfig{Kind: "manual"}),
		node("kick_off", flow.TypeRunFlow, flow.RunFlowConfig{FlowID: "child", Wait: flow.RunFlowWaitStart}),
	}, Edges: []flow.Edge{edge("start", "kick_off", "")}}
	require.Len(t, def.Validate(), 1)
	require.Contains(t, def.Validate()[0].Message, "must be abandoned")
}

func TestIDPolicyValidation(t *testing.T) {
	def := initFlow()
	def.Settings.IDConflictPolicy, def.Settings.IDReusePolicy = flow.ConflictFail, flow.ReuseRejectDuplicate
	require.Empty(t, def.Validate())
	def.Settings.IDConflictPolicy, def.Settings.IDReusePolicy = "sometimes", "never"
	require.Equal(t, []string{
		`unknown Workflow ID conflict policy "sometimes"`,
		`unknown Workflow ID reuse policy "never"`,
	}, messages(def.Validate()))
}
