package app

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/flow"
)

// previewTransform builds a Transform step's output against sample data, for
// the mapper, with the Workflow's own code (flow.BuildMapped).
func (s *Server) previewTransform(w http.ResponseWriter, r *http.Request, _ user) {
	var in struct {
		Config flow.TransformConfig `json:"config"`
		Env    map[string]any       `json:"env"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	sampleTimes(in.Env)
	out, errs := flow.PreviewMapped(in.Config.Fields, in.Config.Mapping, in.Config.Lists, in.Env)
	writeJSON(w, http.StatusOK, map[string]any{"argument": out, "errors": errs})
}

// sampleTimes gives sample data the types a run has: run.startedAt is a
// time, so templates can call its methods (run.startedAt.Format(...)); JSON
// brings it as text.
func sampleTimes(env map[string]any) {
	if run, ok := env["run"].(map[string]any); ok {
		if s, ok := run["startedAt"].(string); ok {
			if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
				run["startedAt"] = t
			}
		}
	}
}

// previewConnector builds a connector step's Activity argument against sample
// data, for the mapper. It runs the Workflow's own code (flow.ConnectorArgument)
// in the app, with nothing to call: rendering is deterministic and has no I/O.
func (s *Server) previewConnector(w http.ResponseWriter, r *http.Request, _ user) {
	var in struct {
		Type   string          `json:"type"`
		Config json.RawMessage `json:"config"`
		Env    map[string]any  `json:"env"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if !flow.IsConnector(flow.NodeType(in.Type)) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "not a connector step: " + in.Type})
		return
	}
	sampleTimes(in.Env)
	node := flow.Node{ID: "preview", Type: flow.NodeType(in.Type), Key: "preview", Label: "preview", Config: in.Config}
	arg, errs, err := flow.PreviewConnector(node, in.Env)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"argument": nil, "errors": map[string]string{"": err.Error()}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"argument": arg, "errors": errs})
}
