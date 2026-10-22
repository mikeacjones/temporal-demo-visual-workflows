package workflows

import (
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/flow"
)

// bookings is a service where /<thing> books and /<thing>/cancel cancels;
// any path listed in failing answers 402 (a business refusal, not retried).
func bookings(calls *[]string, mu *sync.Mutex, failing ...string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		*calls = append(*calls, r.URL.Path)
		mu.Unlock()
		for _, f := range failing {
			if r.URL.Path == f {
				w.WriteHeader(http.StatusPaymentRequired)
				return
			}
		}
		_, _ = w.Write([]byte(`{"id": "` + strings.TrimPrefix(r.URL.Path, "/") + `-1"}`))
	})
}

func post(path string) flow.HTTPConfig {
	return flow.HTTPConfig{Method: "POST", URL: baseURL + path}
}

// trip is flight → hotel → notify → charge, with a saga over all but
// notify's undo. Members are configurable per test.
func trip(members []flow.SagaMember, extra ...flow.Node) flow.Definition {
	nodes := []flow.Node{
		node("start", flow.TypeTrigger, flow.TriggerConfig{Kind: "manual"}),
		node("flight", flow.TypeHTTP, post("/flight")),
		node("hotel", flow.TypeHTTP, post("/hotel")),
		node("notify", flow.TypeHTTP, post("/notify")),
		node("charge", flow.TypeHTTP, post("/charge")),
		node("cancel_flight", flow.TypeHTTP, post("/flight/cancel")),
		node("cancel_hotel", flow.TypeHTTP, post("/hotel/cancel")),
		node("saga", flow.TypeSaga, flow.SagaConfig{Members: members}),
	}
	return flow.Definition{ID: "f", Name: "trip", Nodes: append(nodes, extra...), Edges: []flow.Edge{
		edge("start", "flight", ""), edge("flight", "hotel", ""), edge("hotel", "notify", ""), edge("notify", "charge", ""),
	}}
}

var tripMembers = []flow.SagaMember{
	{Step: "flight", Unwinds: true, Compensation: "cancel_flight"},
	{Step: "hotel", Unwinds: true, Compensation: "cancel_hotel"},
	{Step: "notify", Unwinds: false},
	{Step: "charge", Unwinds: true},
}

func TestSagaUnwindsNewestFirst(t *testing.T) {
	var calls []string
	var mu sync.Mutex
	env := newEnv(t, bookings(&calls, &mu, "/charge"))
	def := trip(tripMembers)
	require.Empty(t, def.Validate())

	env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{Flow: def})
	require.Error(t, env.GetWorkflowError(), "nothing is connected to compensated, so the run fails after unwinding")
	require.Equal(t, []string{"/flight", "/hotel", "/notify", "/charge", "/hotel/cancel", "/flight/cancel"}, calls)
}

func TestSagaCompensatedOutcomeCompletesTheRun(t *testing.T) {
	var calls []string
	var mu sync.Mutex
	env := newEnv(t, bookings(&calls, &mu, "/charge"))
	def := trip(tripMembers, node("sorry", flow.TypeTransform, flow.TransformConfig{Output: `{"msg": "Trip cancelled: {{ steps.saga.error }} at {{ steps.saga.failedStep }}", "undone": "{{ steps.saga.compensated }}"}`}))
	def.Edges = append(def.Edges, edge("saga", "sorry", flow.HandleCompensated))
	require.Empty(t, def.Validate())

	env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{Flow: def})
	require.NoError(t, env.GetWorkflowError())
	st := runState(t, env)
	require.Equal(t, flow.RunCompleted, st.Status)
	require.Contains(t, string(st.Nodes["sorry"].Output), "at charge")
	require.Contains(t, string(st.Nodes["sorry"].Output), `"undone":2`)
	require.Equal(t, flow.SagaState{Registered: []string{"flight", "hotel"}, Status: "compensated", Trigger: "charge", Error: st.Sagas["saga"].Error}, st.Sagas["saga"])
	require.Equal(t, flow.NodeFailed, st.Nodes["charge"].Status)
	require.Equal(t, flow.NodeCompleted, st.Nodes["cancel_hotel"].Status)
}

func TestHandledMemberFailureCarriesOn(t *testing.T) {
	var calls []string
	var mu sync.Mutex
	env := newEnv(t, bookings(&calls, &mu, "/notify"))
	def := trip(tripMembers)

	env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{Flow: def})
	require.NoError(t, env.GetWorkflowError())
	st := runState(t, env)
	require.Equal(t, []string{"/flight", "/hotel", "/notify", "/charge"}, calls, "notify's failure doesn't unwind, and charge still runs")
	require.Contains(t, st.Nodes["notify"].Error, "402")
	require.Equal(t, "", st.Sagas["saga"].Status)
}

func TestTriggerOnlyMemberUnwindsWithoutAnUndoOfItsOwn(t *testing.T) {
	var calls []string
	var mu sync.Mutex
	env := newEnv(t, bookings(&calls, &mu, "/hotel"))
	env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{Flow: trip(tripMembers)})
	require.Error(t, env.GetWorkflowError())
	// The hotel failed, so only the flight was registered and undone.
	require.Equal(t, []string{"/flight", "/hotel", "/flight/cancel"}, calls)
}

func TestNonMemberFailureDoesNotUnwind(t *testing.T) {
	var calls []string
	var mu sync.Mutex
	env := newEnv(t, bookings(&calls, &mu, "/charge"))
	// charge isn't a member here: its failure fails the run without undoing.
	env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{Flow: trip(tripMembers[:3])})
	require.Error(t, env.GetWorkflowError())
	require.Equal(t, []string{"/flight", "/hotel", "/notify", "/charge"}, calls)
}

func TestFailedCompensationTakesTheFailedOutcome(t *testing.T) {
	var calls []string
	var mu sync.Mutex
	env := newEnv(t, bookings(&calls, &mu, "/charge", "/hotel/cancel"))
	def := trip(tripMembers,
		node("page", flow.TypeTransform, flow.TransformConfig{Output: `{"failures": "{{ steps.saga.failures }}"}`}),
		node("tell_ops", flow.TypeTransform, flow.TransformConfig{Output: `{"by": "{{ steps.cancel_hotel.error }}"}`}),
	)
	def.Edges = append(def.Edges, edge("saga", "page", flow.HandleFailed), edge("cancel_hotel", "tell_ops", flow.HandleError))
	require.Empty(t, def.Validate())

	env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{Flow: def})
	// The hotel's cancel failed but routed its error within its chain, so
	// its compensation succeeded, and the flight's ran too. Compensated
	// isn't connected, so the run fails as the charge did.
	require.ErrorContains(t, env.GetWorkflowError(), "402")
	require.Contains(t, calls, "/flight/cancel")

	// Without the error edge the hotel's compensation fails, and the saga
	// takes its failed outcome.
	calls = nil
	env = newEnv(t, bookings(&calls, &mu, "/charge", "/hotel/cancel"))
	def.Edges = def.Edges[:len(def.Edges)-1]
	env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{Flow: def})
	require.NoError(t, env.GetWorkflowError())
	st := runState(t, env)
	require.Equal(t, "failed", st.Sagas["saga"].Status)
	require.Contains(t, string(st.Nodes["page"].Output), "hotel")
	require.Contains(t, calls, "/flight/cancel", "a failed compensation doesn't stop the others")
}

func TestBuiltInRollbackRefundsACharge(t *testing.T) {
	sb := newSandbox(t)
	def := definition(
		connector("customer", "stripe", "create_customer", nil, map[string]string{"email": "hank@globex.com"}),
		connector("charge", "stripe", "charge", nil, map[string]string{"amount": "4800", "currency": "usd", "customer": "{{ steps.customer.id }}", "paymentMethod": "pm_card_visa"}),
		node("ship", flow.TypeHTTP, post("/fail")),
		node("saga", flow.TypeSaga, flow.SagaConfig{Members: []flow.SagaMember{
			{Step: "customer", Unwinds: true, BuiltIn: true},
			{Step: "charge", Unwinds: true, BuiltIn: true},
			{Step: "ship", Unwinds: true},
		}}),
	)
	def.Edges = chain("customer", "charge", "ship")
	_, err := sb.run(t, def, nil)
	require.Error(t, err)

	intents := sb.stripe.Objects("payment_intent")
	require.Len(t, intents, 1)
	require.EqualValues(t, 4800, sb.stripe.Refunded(intents[0]["id"].(string)))
	require.Empty(t, sb.stripe.Objects("customer"))
}

func TestSagaValidation(t *testing.T) {
	// A trip whose unused compensation steps are left out, so each case
	// shows the one problem it's about.
	base := func(members []flow.SagaMember, extra ...flow.Edge) flow.Definition {
		def := trip(members)
		used := map[string]bool{}
		for _, m := range members {
			used[m.Compensation] = true
		}
		for _, e := range extra {
			used[e.Target] = true
		}
		var nodes []flow.Node
		for _, n := range def.Nodes {
			if !strings.HasPrefix(n.ID, "cancel_") || used[n.ID] {
				nodes = append(nodes, n)
			}
		}
		def.Nodes = nodes
		def.Edges = append(def.Edges, extra...)
		return def
	}
	for _, tc := range []struct {
		def     flow.Definition
		problem string
	}{
		{flow.Definition{ID: "f", Name: "empty", Nodes: []flow.Node{node("start", flow.TypeTrigger, flow.TriggerConfig{Kind: "manual"}), node("saga", flow.TypeSaga, flow.SagaConfig{})}}, "has no members"},
		{base([]flow.SagaMember{{Step: "flight", Unwinds: true, Compensation: "cancel_flight"}}, edge("start", "cancel_flight", "")), "nothing can lead into cancel_flight"},
		{trip([]flow.SagaMember{{Step: "flight", Unwinds: true}}), "cancel_flight is not connected"},
		{base([]flow.SagaMember{{Step: "flight", Unwinds: true, Compensation: "cancel_hotel"}, {Step: "hotel", Unwinds: true, Compensation: "cancel_hotel"}}), "already compensates"},
		{base([]flow.SagaMember{{Step: "flight", Unwinds: true, Compensation: "cancel_flight"}, {Step: "cancel_flight"}}), "compensates a step"},
		{base([]flow.SagaMember{{Step: "flight", Unwinds: true, BuiltIn: true}, {Step: "hotel", Compensation: "cancel_hotel"}, {Step: "notify", Compensation: "cancel_flight"}}), "has no built-in rollback"},
		{base([]flow.SagaMember{{Step: "flight", Unwinds: true, Compensation: "cancel_flight"}, {Step: "hotel", Compensation: "cancel_hotel"}}, edge("flight", "hotel", flow.HandleError)), "can't also have an error outcome"},
	} {
		problems := tc.def.Validate()
		found := false
		for _, p := range problems {
			found = found || strings.Contains(p.Message, tc.problem)
		}
		require.True(t, found, "want %q in %+v", tc.problem, problems)
	}
}
