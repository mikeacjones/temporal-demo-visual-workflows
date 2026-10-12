package app

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"

	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/examples"
	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/flow"
	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/workflows"
)

// Anonymous sessions' libraries close after 30 idle days; signed-in users'
// libraries are kept.
const anonymousLibraryIdleSeconds = 30 * 24 * 3600

// ensureLibrary starts the user's library Workflow if it isn't running. A new
// user starts with the example flows.
func (s *Server) ensureLibrary(ctx context.Context, u user) error {
	idle := anonymousLibraryIdleSeconds
	if u.SignedIn {
		idle = 0
	}
	_, err := s.tc.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:                       s.libraryID(u.Key),
		TaskQueue:                s.cfg.LibraryTaskQueue,
		WorkflowIDConflictPolicy: enumspb.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING,
	}, workflows.LibraryWorkflow, workflows.LibraryInput{Flows: examples.Flows(s.cfg.SandboxURL, time.Now()), IdleTimeoutSeconds: idle})
	return err
}

func (s *Server) flows(ctx context.Context, u user) ([]flow.Definition, error) {
	if err := s.ensureLibrary(ctx, u); err != nil {
		return nil, err
	}
	v, err := s.tc.QueryWorkflow(ctx, s.libraryID(u.Key), "", workflows.QueryFlows)
	if err != nil {
		return nil, err
	}
	var list []flow.Definition
	return list, v.Get(&list)
}

func (s *Server) findFlow(ctx context.Context, u user, id string) (flow.Definition, bool, error) {
	list, err := s.flows(ctx, u)
	if err != nil {
		return flow.Definition{}, false, err
	}
	for _, f := range list {
		if f.ID == id {
			return f, true, nil
		}
	}
	return flow.Definition{}, false, nil
}

func (s *Server) listFlows(w http.ResponseWriter, r *http.Request, u user) {
	list, err := s.flows(r.Context(), u)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	out := make([]map[string]any, 0, len(list))
	for _, f := range list {
		out = append(out, map[string]any{
			"id": f.ID, "name": f.Name, "description": f.Description, "version": f.Version,
			"updatedAt": f.UpdatedAt, "triggerKind": triggerOf(f).Kind, "nodeCount": len(f.Nodes),
			"scheduleActive": s.scheduleActive(r.Context(), u, f.ID), "keyed": f.Settings.KeyedBy != "",
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getFlow(w http.ResponseWriter, r *http.Request, u user) {
	f, ok, err := s.findFlow(r.Context(), u, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("flow not found"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"flow":           f,
		"webhookUrl":     baseURL(r) + "/hooks/" + s.webhookToken(u.Key, f.ID),
		"scheduleActive": s.scheduleActive(r.Context(), u, f.ID),
	})
}

func (s *Server) createFlow(w http.ResponseWriter, r *http.Request, u user) {
	var def flow.Definition
	if err := decodeBody(r, &def); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	def.ID = "f-" + randomSuffix()
	s.store(w, r, u, def)
}

func (s *Server) saveFlow(w http.ResponseWriter, r *http.Request, u user) {
	var def flow.Definition
	if err := decodeBody(r, &def); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	def.ID = r.PathValue("id")
	s.store(w, r, u, def)
}

func (s *Server) store(w http.ResponseWriter, r *http.Request, u user, def flow.Definition) {
	saved, problems, err := s.save(r.Context(), u, def)
	switch {
	case len(problems) > 0:
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "flow is invalid", "problems": problems})
		return
	case err != nil:
		writeError(w, http.StatusBadRequest, err)
		return
	}
	// An active schedule should start the version just saved.
	if s.scheduleActive(r.Context(), u, saved.ID) {
		if err := s.upsertSchedule(r.Context(), r, u, saved); err != nil {
			s.log.Warn("could not update schedule", "flow", saved.ID, "error", err)
		}
	}
	writeJSON(w, http.StatusOK, saved)
}

func (s *Server) save(ctx context.Context, u user, def flow.Definition) (flow.Definition, []flow.Problem, error) {
	if !flowIDPattern.MatchString(def.ID) {
		return def, nil, errors.New("invalid flow id")
	}
	// Validate here for a fast, detailed 400. The library's Update validator
	// enforces the same rules durably.
	if problems := def.Validate(); len(problems) > 0 {
		return def, problems, nil
	}
	if err := s.ensureLibrary(ctx, u); err != nil {
		return def, nil, err
	}
	handle, err := s.tc.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
		WorkflowID:   s.libraryID(u.Key),
		UpdateName:   workflows.UpdateSaveFlow,
		Args:         []any{def},
		WaitForStage: client.WorkflowUpdateStageCompleted,
	})
	var saved flow.Definition
	if err == nil {
		err = handle.Get(ctx, &saved)
	}
	return saved, nil, err
}

func (s *Server) deleteFlow(w http.ResponseWriter, r *http.Request, u user) {
	id := r.PathValue("id")
	if err := s.ensureLibrary(r.Context(), u); err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	handle, err := s.tc.UpdateWorkflow(r.Context(), client.UpdateWorkflowOptions{
		WorkflowID: s.libraryID(u.Key), UpdateName: workflows.UpdateDeleteFlow,
		Args: []any{id}, WaitForStage: client.WorkflowUpdateStageCompleted,
	})
	if err == nil {
		err = handle.Get(r.Context(), nil)
	}
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	_ = s.tc.ScheduleClient().GetHandle(r.Context(), s.scheduleID(u.Key, id)).Delete(r.Context())
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) versions(ctx context.Context, u user, id string) ([]flow.Definition, error) {
	if err := s.ensureLibrary(ctx, u); err != nil {
		return nil, err
	}
	v, err := s.tc.QueryWorkflow(ctx, s.libraryID(u.Key), "", workflows.QueryVersions, id)
	if err != nil {
		return nil, err
	}
	var list []flow.Definition
	return list, v.Get(&list)
}

func (s *Server) listVersions(w http.ResponseWriter, r *http.Request, u user) {
	list, err := s.versions(r.Context(), u, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	out := []map[string]any{}
	for _, d := range list {
		out = append(out, map[string]any{"version": d.Version, "updatedAt": d.UpdatedAt, "nodeCount": len(d.Nodes)})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getVersion(w http.ResponseWriter, r *http.Request, u user) {
	want, _ := strconv.Atoi(r.PathValue("version"))
	list, err := s.versions(r.Context(), u, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	for _, d := range list {
		if d.Version == want {
			writeJSON(w, http.StatusOK, d)
			return
		}
	}
	writeError(w, http.StatusNotFound, errors.New("version not found"))
}

// --- templates ----------------------------------------------------------------

const builtInPrefix = "builtin-"

func (s *Server) ensureGallery(ctx context.Context) error {
	_, err := s.tc.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:                       s.galleryID(),
		TaskQueue:                s.cfg.LibraryTaskQueue,
		WorkflowIDConflictPolicy: enumspb.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING,
	}, workflows.GalleryWorkflow, workflows.GalleryInput{})
	return err
}

func (s *Server) gallery(ctx context.Context) ([]workflows.Template, error) {
	if err := s.ensureGallery(ctx); err != nil {
		return nil, err
	}
	v, err := s.tc.QueryWorkflow(ctx, s.galleryID(), "", workflows.QueryTemplates)
	if err != nil {
		return nil, err
	}
	var list []workflows.Template
	return list, v.Get(&list)
}

func (s *Server) templateView(t workflows.Template, u user) map[string]any {
	return map[string]any{
		"id": t.ID, "name": t.Flow.Name, "description": t.Flow.Description, "author": t.Author,
		"builtIn": strings.HasPrefix(t.ID, builtInPrefix), "listed": t.Listed, "mine": t.AuthorKey == u.Key,
		"publishedAt": t.PublishedAt, "nodeCount": len(t.Flow.Nodes), "flow": t.Flow,
	}
}

func (s *Server) builtIns() []workflows.Template {
	var list []workflows.Template
	for _, def := range examples.Flows(s.cfg.SandboxURL, s.started) {
		list = append(list, workflows.Template{ID: builtInPrefix + def.ID, Flow: def, Author: "Temporal", Listed: true, PublishedAt: def.UpdatedAt})
	}
	// Same order as a new user's flow list.
	slices.SortFunc(list, func(a, b workflows.Template) int { return b.PublishedAt.Compare(a.PublishedAt) })
	return list
}

func (s *Server) listTemplates(w http.ResponseWriter, r *http.Request, u user) {
	shared, err := s.gallery(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	out := []map[string]any{}
	for _, t := range shared {
		if t.Listed || t.AuthorKey == u.Key {
			out = append(out, s.templateView(t, u))
		}
	}
	for _, t := range s.builtIns() {
		out = append(out, s.templateView(t, u))
	}
	writeJSON(w, http.StatusOK, out)
}

// findTemplate returns a template the caller may see: built in, listed, their
// own, or unlisted with a valid share token.
func (s *Server) findTemplate(ctx context.Context, u user, id, token string) (workflows.Template, bool) {
	for _, t := range s.builtIns() {
		if t.ID == id {
			return t, true
		}
	}
	shared, err := s.gallery(ctx)
	if err != nil {
		return workflows.Template{}, false
	}
	for _, t := range shared {
		if t.ID != id {
			continue
		}
		if v, ok := s.verify(token); t.Listed || t.AuthorKey == u.Key || (ok && v == "tpl:"+id) {
			return t, true
		}
	}
	return workflows.Template{}, false
}

func (s *Server) publishTemplate(w http.ResponseWriter, r *http.Request, u user) {
	var body struct {
		FlowID string `json:"flowId"`
		Listed bool   `json:"listed"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	def, ok, err := s.findFlow(r.Context(), u, body.FlowID)
	if err != nil || !ok {
		writeError(w, http.StatusNotFound, errors.New("flow not found"))
		return
	}
	if err := s.ensureGallery(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	t := workflows.Template{ID: "t-" + randomSuffix(), Flow: def, Author: u.Name, AuthorKey: u.Key, Listed: body.Listed}
	handle, err := s.tc.UpdateWorkflow(r.Context(), client.UpdateWorkflowOptions{
		WorkflowID: s.galleryID(), UpdateName: workflows.UpdatePublish, Args: []any{t},
		WaitForStage: client.WorkflowUpdateStageCompleted,
	})
	if err == nil {
		err = handle.Get(r.Context(), &t)
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"template": s.templateView(t, u),
		"shareUrl": baseURL(r) + "/templates/" + t.ID + "?t=" + s.sign("tpl:"+t.ID),
	})
}

func (s *Server) getTemplate(w http.ResponseWriter, r *http.Request, u user) {
	t, ok := s.findTemplate(r.Context(), u, r.PathValue("id"), r.URL.Query().Get("t"))
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("template not found"))
		return
	}
	writeJSON(w, http.StatusOK, s.templateView(t, u))
}

// useTemplate copies a template into the caller's own flows. The copy is
// independent: later changes to the template don't affect it.
func (s *Server) useTemplate(w http.ResponseWriter, r *http.Request, u user) {
	var body struct {
		T string `json:"t"`
	}
	_ = decodeBody(r, &body)
	t, ok := s.findTemplate(r.Context(), u, r.PathValue("id"), body.T)
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("template not found"))
		return
	}
	def := t.Flow
	def.ID = "f-" + randomSuffix()
	s.store(w, r, u, def)
}

func (s *Server) unpublishTemplate(w http.ResponseWriter, r *http.Request, u user) {
	handle, err := s.tc.UpdateWorkflow(r.Context(), client.UpdateWorkflowOptions{
		WorkflowID: s.galleryID(), UpdateName: workflows.UpdateUnpublish, Args: []any{r.PathValue("id"), u.Key},
		WaitForStage: client.WorkflowUpdateStageCompleted,
	})
	if err == nil {
		err = handle.Get(r.Context(), nil)
	}
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func triggerOf(def flow.Definition) flow.TriggerConfig {
	for _, n := range def.Nodes {
		if n.Type == flow.TypeTrigger {
			c, _ := flow.DecodeConfig[flow.TriggerConfig](n)
			return c
		}
	}
	return flow.TriggerConfig{}
}
