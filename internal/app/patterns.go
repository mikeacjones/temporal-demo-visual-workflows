package app

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/flow"
	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/patterns"
)

// The design pattern library is static content: each pattern, how to build it,
// and an example flow pointing at this deployment's sandbox.

func (s *Server) listPatterns(w http.ResponseWriter, r *http.Request, u user) {
	writeJSON(w, http.StatusOK, map[string]any{"categories": patterns.Categories, "patterns": patterns.All(s.cfg.SandboxURL)})
}

func (s *Server) findPattern(id string) (patterns.Pattern, bool) {
	for _, p := range patterns.All(s.cfg.SandboxURL) {
		if p.ID == id {
			return p, true
		}
	}
	return patterns.Pattern{}, false
}

func (s *Server) getPattern(w http.ResponseWriter, r *http.Request, u user) {
	p, ok := s.findPattern(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("pattern not found"))
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// usePattern copies a pattern's example into the user's library. Child flows
// are saved first, under new IDs, and the example's Run flow and For each
// steps are pointed at those copies.
func (s *Server) usePattern(w http.ResponseWriter, r *http.Request, u user) {
	p, ok := s.findPattern(r.PathValue("id"))
	if !ok || p.Flow == nil {
		writeError(w, http.StatusNotFound, errors.New("pattern has no example flow"))
		return
	}
	ids := map[string]string{}
	for _, child := range p.Children {
		ids[child.ID] = "f-" + randomSuffix()
		child.ID = ids[child.ID]
		if _, problems, err := s.save(r.Context(), u, child); err != nil || len(problems) > 0 {
			writeError(w, http.StatusBadGateway, errors.Join(errors.New("could not save child flow "+child.Name), err))
			return
		}
	}
	def := *p.Flow
	def.ID = "f-" + randomSuffix()
	def.Nodes = append([]flow.Node(nil), def.Nodes...)
	for i, n := range def.Nodes {
		def.Nodes[i].Config = repointChild(n, ids)
	}
	saved, problems, err := s.save(r.Context(), u, def)
	switch {
	case len(problems) > 0:
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "flow is invalid", "problems": problems})
	case err != nil:
		writeError(w, http.StatusBadGateway, err)
	default:
		writeJSON(w, http.StatusOK, saved)
	}
}

// repointChild rewrites a Run flow or For each step's flowId to the copy saved
// for this user.
func repointChild(n flow.Node, ids map[string]string) json.RawMessage {
	if n.Type != flow.TypeRunFlow && n.Type != flow.TypeForEach {
		return n.Config
	}
	var c map[string]any
	if json.Unmarshal(n.Config, &c) != nil {
		return n.Config
	}
	if id, ok := c["flowId"].(string); ok && ids[id] != "" {
		c["flowId"] = ids[id]
	}
	raw, _ := json.Marshal(c)
	return raw
}
