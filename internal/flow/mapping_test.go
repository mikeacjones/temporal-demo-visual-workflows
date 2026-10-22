package flow

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/connectors"
)

var orderFields = []connectors.Field{
	{Name: "orderId", Type: connectors.String, Required: true},
	{Name: "totalCents", Type: connectors.Integer},
	{Name: "customer", Type: connectors.Object, Fields: []connectors.Field{
		{Name: "email", Type: connectors.String}, {Name: "vip", Type: connectors.Boolean},
	}},
	{Name: "lines", Type: connectors.Array, Fields: []connectors.Field{
		{Name: "sku", Type: connectors.String}, {Name: "qty", Type: connectors.Integer}, {Name: "position", Type: connectors.Integer},
	}},
	{Name: "skus", Type: connectors.Array, Items: connectors.String},
}

var orderEnv = map[string]any{"input": map[string]any{
	"id": 42, "amount": "19.99", "email": "ADA@EXAMPLE.COM", "tier": "gold",
	"items": []any{
		map[string]any{"code": "BLND-500", "count": "2"},
		map[string]any{"code": "GIFT-WRAP", "count": 1},
		map[string]any{"code": "KTL-200", "count": 3.0},
	},
}}

func TestBuildMappedShapesOutput(t *testing.T) {
	out, err := BuildMapped(orderFields, map[string]string{
		"orderId":          "A-{{ input.id }}",
		"totalCents":       "{{ float(input.amount) * 100 }}",
		"customer.email":   "{{ lower(input.email) }}",
		"customer.vip":     `{{ input.tier == "gold" }}`,
		"lines[].sku":      "{{ it.code }}",
		"lines[].qty":      "{{ it.count }}",
		"lines[].position": "{{ itIndex + 1 }}",
		"skus[]":           "{{ lower(it.code) }}",
	}, map[string]ListMapping{
		"lines": {From: "input.items", Where: `it.code != "GIFT-WRAP"`},
		"skus":  {From: "input.items"},
	}, orderEnv)
	require.NoError(t, err)
	require.Equal(t, map[string]any{
		"orderId":    "A-42",
		"totalCents": 1999.0,
		"customer":   map[string]any{"email": "ada@example.com", "vip": true},
		"lines": []any{
			map[string]any{"sku": "BLND-500", "qty": 2.0, "position": 1.0},
			map[string]any{"sku": "KTL-200", "qty": 3.0, "position": 3.0},
		},
		"skus": []any{"blnd-500", "gift-wrap", "ktl-200"},
	}, out)
}

func TestValidateMapped(t *testing.T) {
	ok := map[string]string{"orderId": "x"}
	for _, tc := range []struct {
		fields  []connectors.Field
		mapping map[string]string
		lists   map[string]ListMapping
		problem string
	}{
		{orderFields, map[string]string{}, nil, "needs orderId mapped"},
		{orderFields, map[string]string{"orderId": "x", "nope": "y"}, nil, "nope isn't an output field"},
		{orderFields, map[string]string{"orderId": "x", "lines[].sku": "y"}, nil, "isn't mapped from a list"},
		{orderFields, ok, map[string]ListMapping{"orderId": {From: "input.items"}}, "only a list field"},
		{orderFields, ok, map[string]ListMapping{"lines": {From: "input.items +"}}, "lines from"},
		{[]connectors.Field{{Name: "bad name", Type: connectors.String}}, nil, nil, "needs a name"},
		{[]connectors.Field{{Name: "a", Type: connectors.String}, {Name: "a", Type: connectors.Number}}, nil, nil, "appears twice"},
		{[]connectors.Field{{Name: "a", Type: "money"}}, nil, nil, "unknown type"},
	} {
		err := validateMapped(tc.fields, tc.mapping, tc.lists)
		require.ErrorContains(t, err, tc.problem)
	}
	require.NoError(t, validateMapped(orderFields, map[string]string{"orderId": "x", "lines[].sku": "{{ it.code }}"}, map[string]ListMapping{"lines": {From: "input.items"}}))
}

func TestPreviewMappedReportsEachField(t *testing.T) {
	out, errs := PreviewMapped(orderFields, map[string]string{
		"orderId": "A-{{ input.id }}", "totalCents": "{{ input.nope.deeper }}", "lines[].sku": "{{ it.code }}",
	}, map[string]ListMapping{"lines": {From: "input.items"}}, orderEnv)
	require.Equal(t, "A-42", out["orderId"])
	require.Len(t, out["lines"], 3)
	require.Contains(t, errs["totalCents"], "deeper")
	require.Len(t, errs, 1)
}

func TestAnyKeepsWhatATemplateRenders(t *testing.T) {
	fields := []connectors.Field{{Name: "whole", Type: connectors.Any}, {Name: "n", Type: connectors.Any}, {Name: "tags", Type: connectors.Array}}
	out, err := BuildMapped(fields, map[string]string{"whole": "{{ input.items[0] }}", "n": "{{ input.id }}", "tags[]": "{{ it.code }}"},
		map[string]ListMapping{"tags": {From: "input.items"}}, orderEnv)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"code": "BLND-500", "count": "2"}, out["whole"])
	require.Equal(t, 42, out["n"])
	require.Equal(t, []any{"BLND-500", "GIFT-WRAP", "KTL-200"}, out["tags"])
}
