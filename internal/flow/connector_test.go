package flow

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/connectors"
)

func TestConnectorArgumentTypesEachInput(t *testing.T) {
	conn, _ := connectors.Get("salesforce")
	op, _ := conn.Operation("create")
	c := ConnectorConfig{Settings: map[string]any{"object": "Opportunity"}, Mapping: map[string]string{
		"values.Name":        "{{ input.company }} — {{ input.plan }}",
		"values.Amount":      "{{ input.amount }}", // "480" from a form is a number here
		"values.Probability": "75",
		"values.StageName":   "Closed Won",
		"values.Region__c":   "{{ input.region }}",  // a custom field keeps its type
		"values.Description": "{{ input.missing }}", // nothing to map: left out
	}}
	env := map[string]any{"input": map[string]any{"company": "Globex", "plan": "Pro", "amount": "480", "region": 3.0}}
	arg, err := ConnectorArgument(conn, op, c, env)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"object": "Opportunity", "values": map[string]any{
		"Name": "Globex — Pro", "Amount": 480.0, "Probability": 75.0, "StageName": "Closed Won", "Region__c": 3.0,
	}}, arg)

	gh, _ := connectors.Get("github")
	issue, _ := gh.Operation("create_issue")
	arg, err = ConnectorArgument(gh, issue, ConnectorConfig{Settings: map[string]any{"repo": "acme/storefront"}, Mapping: map[string]string{
		"title": "{{ 42 }}", "labels": "incident, sev{{ input.sev }}", "assignees": "{{ input.people }}",
	}}, map[string]any{"input": map[string]any{"sev": 1, "people": []any{"riley", "dana"}}})
	require.NoError(t, err)
	require.Equal(t, map[string]any{"repo": "acme/storefront", "title": "42", "labels": []any{"incident", "sev1"}, "assignees": []any{"riley", "dana"}}, arg)

	stripe, _ := connectors.Get("stripe")
	charge, _ := stripe.Operation("charge")
	_, err = ConnectorArgument(stripe, charge, ConnectorConfig{Mapping: map[string]string{"amount": "{{ input.note }}"}}, map[string]any{"input": map[string]any{"note": "lots"}})
	require.ErrorContains(t, err, `"lots" is not a number`)
}

func TestConnectorStepsSavedWithParams(t *testing.T) {
	raw := `{"operation": "upsert", "params": {"object": "Account", "externalIdField": "Domain__c", "externalId": "{{ input.domain }}",
		"values": "{\"Name\": \"{{ input.company }}\", \"Rating\": \"Hot\", \"NumberOfEmployees\": 12}"}, "activity": {"retry": {}}}`
	n := Node{ID: "a", Type: "salesforce", Key: "a", Label: "a", Config: json.RawMessage(raw)}
	_, _, c, err := ConnectorStep(n)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"object": "Account", "externalIdField": "Domain__c"}, c.Settings)
	require.Equal(t, map[string]string{
		"externalId": "{{ input.domain }}", "values.Name": "{{ input.company }}", "values.Rating": "Hot", "values.NumberOfEmployees": "12",
	}, c.Mapping)
	require.NoError(t, validateConnector(n))
}

func TestPreviewReportsEachInputOnItsOwn(t *testing.T) {
	raw, _ := json.Marshal(ConnectorConfig{Operation: "charge", Mapping: map[string]string{
		"amount": "{{ input.amount * 100 }}", "currency": "usd", "paymentMethod": "pm_card_visa",
		"description": "{{ input.note }}", "metadata.plan": "{{ input.plan.name }}",
	}})
	n := Node{ID: "p", Type: "stripe", Key: "p", Label: "p", Config: raw}
	arg, errs, err := PreviewConnector(n, map[string]any{"input": map[string]any{"amount": 48.5, "note": 7, "plan": "pro"}})
	require.NoError(t, err)
	require.Equal(t, map[string]any{"amount": 4850.0, "currency": "usd", "paymentMethod": "pm_card_visa", "description": "7"}, arg)
	require.Len(t, errs, 1)
	require.Contains(t, errs["metadata.plan"], "name")
}
