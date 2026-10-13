package flow

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// An entity needn't be keyed: events reach it by its Workflow ID.
func TestEntityWithoutAKey(t *testing.T) {
	d := Definition{ID: "f", Name: "f", Settings: FlowSettings{Entity: true}, Nodes: []Node{
		{ID: "start", Type: TypeTrigger, Key: "start", Label: "start", Config: json.RawMessage(`{"kind":"manual"}`)},
	}}
	require.Empty(t, d.Validate())
}

// Flows saved before the Entity setting: a keyed flow without an idle
// timeout was an entity, and one with an idle timeout was not.
func TestSettingsFromBeforeEntity(t *testing.T) {
	for raw, want := range map[string]bool{
		`{"keyedBy":"input.id"}`:                          true,
		`{"keyedBy":"input.id","idleTimeoutSeconds":600}`: false,
		`{}`:                                    false,
		`{"keyedBy":"input.id","entity":false}`: false,
		`{"keyedBy":"input.id","entity":true}`:  true,
	} {
		var s FlowSettings
		require.NoError(t, json.Unmarshal([]byte(raw), &s))
		require.Equal(t, want, s.Entity, raw)
	}
	out, _ := json.Marshal(FlowSettings{KeyedBy: "input.id"})
	var back FlowSettings
	require.NoError(t, json.Unmarshal(out, &back))
	require.False(t, back.Entity, "a flow saved now keeps Entity off")
}
