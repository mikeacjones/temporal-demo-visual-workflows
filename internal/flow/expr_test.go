package flow

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRender(t *testing.T) {
	env := Env(map[string]any{"id": "A1"}, map[string]any{"order": map[string]any{"body": map[string]any{"total": 120.0}}})

	v, err := Render("{{ steps.order.body.total }}", env)
	require.NoError(t, err)
	require.Equal(t, 120.0, v, "a lone placeholder keeps its type")

	s, err := RenderString("/orders/{{ input.id }}?min={{ steps.order.body.total - 20 }}", env)
	require.NoError(t, err)
	require.Equal(t, "/orders/A1?min=100", s)
}

func TestClockIsNotAvailableToExpressions(t *testing.T) {
	// now() would make a replay disagree with the original run.
	_, err := Eval("now()", Env(nil, nil))
	require.Error(t, err)
}
