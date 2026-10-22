package workflows

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/connectors"
	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/flow"
)

func TestTransformMapsFieldsAndLists(t *testing.T) {
	env := newEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"records": [{"Name": "Anvils", "Amount": 84000}, {"Name": "Skates", "Amount": 23500}, {"Name": "Pilot", "Amount": 900}]}`))
	}))
	def := flow.Definition{ID: "f", Name: "transform", Nodes: []flow.Node{
		node("start", flow.TypeTrigger, flow.TriggerConfig{Kind: "manual"}),
		node("deals", flow.TypeHTTP, flow.HTTPConfig{Method: "GET", URL: baseURL}),
		node("shape", flow.TypeTransform, flow.TransformConfig{
			Fields: []connectors.Field{
				{Name: "owner", Type: connectors.String, Required: true},
				{Name: "big", Type: connectors.Array, Fields: []connectors.Field{{Name: "title", Type: connectors.String}, {Name: "dollars", Type: connectors.Integer}}},
				{Name: "count", Type: connectors.Integer},
			},
			Mapping: map[string]string{
				"owner": "{{ upper(input.owner) }}", "count": "{{ len(steps.deals.body.records) }}",
				"big[].title": "{{ it.Name }}", "big[].dollars": "{{ it.Amount }}",
			},
			Lists: map[string]flow.ListMapping{"big": {From: "steps.deals.body.records", Where: "it.Amount >= 10000"}},
		}),
		node("use", flow.TypeTransform, flow.TransformConfig{Output: `{"first": "{{ steps.shape.big[0].title }}"}`}),
	}, Edges: []flow.Edge{edge("start", "deals", ""), edge("deals", "shape", ""), edge("shape", "use", "")}}
	require.Empty(t, def.Validate())

	env.ExecuteWorkflow(FlowWorkflow, flow.RunInput{Flow: def, Input: map[string]any{"owner": "riley"}})
	require.NoError(t, env.GetWorkflowError())
	st := runState(t, env)
	require.JSONEq(t, `{"owner": "RILEY", "count": 3, "big": [{"title": "Anvils", "dollars": 84000}, {"title": "Skates", "dollars": 23500}]}`, string(st.Nodes["shape"].Output))
	require.JSONEq(t, `{"first": "Anvils"}`, string(st.Nodes["use"].Output))
}
