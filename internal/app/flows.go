package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"

	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/examples"
	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/flow"
	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/workflows"
)

// Anonymous sessions' libraries close after 30 idle days; signed-in users'
// libraries are kept.
const anonymousLibraryIdleSeconds = 30 * 24 * 3600

// entity says how to start one of the app's entity Workflows (a user's
// library, or the template gallery) if it isn't running yet.
type entity struct {
	opts client.StartWorkflowOptions
	fn   any
	arg  any
}

// library is the user's library. A new user starts with the example flows.
func (s *Server) library(u user) entity {
	idle := anonymousLibraryIdleSeconds
	if u.SignedIn {
		idle = 0
	}
	return entity{
		opts: client.StartWorkflowOptions{
			ID:                       s.libraryID(u.Key),
			TaskQueue:                s.cfg.LibraryTaskQueue,
			WorkflowIDConflictPolicy: enumspb.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING,
		},
		fn:  workflows.LibraryWorkflow,
		arg: workflows.LibraryInput{Flows: examples.Flows(s.cfg.SandboxURL, time.Now()), IdleTimeoutSeconds: idle},
	}
}

func (s *Server) galleryEntity() entity {
	return entity{
		opts: client.StartWorkflowOptions{
			ID:                       s.galleryID(),
			TaskQueue:                s.cfg.LibraryTaskQueue,
			WorkflowIDConflictPolicy: enumspb.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING,
		},
		fn: workflows.GalleryWorkflow, arg: workflows.GalleryInput{},
	}
}

// updateEntity sends an Update to the entity with Update-With-Start: one call that
// starts the Workflow first only if it isn't running.
func (s *Server) updateEntity(ctx context.Context, e entity, name string, args []any, result any) error {
	handle, err := s.tc.UpdateWithStartWorkflow(ctx, client.UpdateWithStartWorkflowOptions{
		StartWorkflowOperation: s.tc.NewWithStartWorkflowOperation(e.opts, e.fn, e.arg),
		UpdateOptions:          client.UpdateWorkflowOptions{UpdateName: name, Args: args, WaitForStage: client.WorkflowUpdateStageCompleted},
	})
	if err != nil {
		return err
	}
	return handle.Get(ctx, result)
}

// queryEntity reads from the entity with a Query. Only when it has never been
// started does it start it and ask again.
func (s *Server) queryEntity(ctx context.Context, e entity, name string, result any, args ...any) error {
	v, err := s.tc.QueryWorkflow(ctx, e.opts.ID, "", name, args...)
	var missing *serviceerror.NotFound
	if errors.As(err, &missing) {
		if _, err = s.tc.ExecuteWorkflow(ctx, e.opts, e.fn, e.arg); err != nil {
			return err
		}
		v, err = s.tc.QueryWorkflow(ctx, e.opts.ID, "", name, args...)
	}
	if err != nil {
		return err
	}
	return v.Get(result)
}

func (s *Server) flows(ctx context.Context, u user) ([]flow.Definition, error) {
	var list []flow.Definition
	return list, s.queryEntity(ctx, s.library(u), workflows.QueryFlows, &list)
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
	active := make([]bool, len(list))
	eachConcurrently(len(list), func(i int) { active[i] = s.scheduleActive(r.Context(), u, list[i].ID) })
	out := make([]map[string]any, 0, len(list))
	for i, f := range list {
		out = append(out, map[string]any{
			"id": f.ID, "name": f.Name, "description": f.Description, "version": f.Version,
			"updatedAt": f.UpdatedAt, "triggerKind": triggerOf(f).Kind, "nodeCount": len(f.Nodes),
			"scheduleActive": active[i], "keyed": f.Settings.KeyedBy != "",
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
	writeJSON(w, http.StatusOK, s.flowDetail(r, u, f, s.scheduleActive(r.Context(), u, f.ID)))
}

func (s *Server) flowDetail(r *http.Request, u user, f flow.Definition, scheduleActive bool) map[string]any {
	return map[string]any{
		"flow":           f,
		"webhookUrl":     baseURL(r) + "/hooks/" + s.webhookToken(u.Key, f.ID),
		"scheduleActive": scheduleActive,
	}
}

func (s *Server) createFlow(w http.ResponseWriter, r *http.Request, u user) {
	var def flow.Definition
	if err := decodeBody(r, &def); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	// The UI names a new flow itself, so it can open the editor before this
	// answers. Creating refuses an ID already in use.
	if !newFlowIDPattern.MatchString(def.ID) {
		def.ID = "f-" + randomSuffix()
	}
	// Answer as GET /api/flows/{id} would. A flow saved just now has no
	// schedule yet.
	if saved, ok := s.store(w, r, u, def, workflows.UpdateCreateFlow); ok {
		writeJSON(w, http.StatusOK, s.flowDetail(r, u, saved, false))
	}
}

func (s *Server) saveFlow(w http.ResponseWriter, r *http.Request, u user) {
	var def flow.Definition
	if err := decodeBody(r, &def); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	def.ID = r.PathValue("id")
	saved, ok := s.store(w, r, u, def, workflows.UpdateSaveFlow)
	if !ok {
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

// store saves (or, with UpdateCreateFlow, creates) the flow, writing the
// error response if it can't.
func (s *Server) store(w http.ResponseWriter, r *http.Request, u user, def flow.Definition, update string) (flow.Definition, bool) {
	saved, problems, err := s.save(r.Context(), u, def, update)
	var appErr *temporal.ApplicationError
	switch {
	case len(problems) > 0:
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "flow is invalid", "problems": problems})
		return saved, false
	case errors.As(err, &appErr) && appErr.Type() == "FlowExists":
		writeError(w, http.StatusConflict, err)
		return saved, false
	case err != nil:
		writeError(w, http.StatusBadRequest, err)
		return saved, false
	}
	return saved, true
}

func (s *Server) save(ctx context.Context, u user, def flow.Definition, update string) (flow.Definition, []flow.Problem, error) {
	if !flowIDPattern.MatchString(def.ID) {
		return def, nil, errors.New("invalid flow id")
	}
	// Validate here for a fast, detailed 400. The library's Update validator
	// enforces the same rules durably.
	if problems := append(def.Validate(), s.limitProblems(def)...); len(problems) > 0 {
		return def, problems, nil
	}
	var saved flow.Definition
	err := s.updateEntity(ctx, s.library(u), update, []any{def}, &saved)
	return saved, nil, err
}

func (s *Server) deleteFlow(w http.ResponseWriter, r *http.Request, u user) {
	id := r.PathValue("id")
	if err := s.updateEntity(r.Context(), s.library(u), workflows.UpdateDeleteFlow, []any{id}, nil); err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	_ = s.tc.ScheduleClient().GetHandle(r.Context(), s.scheduleID(u.Key, id)).Delete(r.Context())
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) versions(ctx context.Context, u user, id string) ([]flow.Definition, error) {
	var list []flow.Definition
	return list, s.queryEntity(ctx, s.library(u), workflows.QueryVersions, &list, id)
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

func (s *Server) gallery(ctx context.Context) ([]workflows.Template, error) {
	var list []workflows.Template
	return list, s.queryEntity(ctx, s.galleryEntity(), workflows.QueryTemplates, &list)
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
	t := workflows.Template{ID: "t-" + randomSuffix(), Flow: def, Author: u.Name, AuthorKey: u.Key, Listed: body.Listed}
	if err := s.updateEntity(r.Context(), s.galleryEntity(), workflows.UpdatePublish, []any{t}, &t); err != nil {
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
	if saved, ok := s.store(w, r, u, def, workflows.UpdateCreateFlow); ok {
		writeJSON(w, http.StatusOK, s.flowDetail(r, u, saved, false))
	}
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

// limitNames lists the rate limits shared by every run.
func (s *Server) limitNames() []string {
	var names []string
	for _, l := range s.cfg.SharedLimits {
		names = append(names, l.Name)
	}
	return names
}

// limitProblems reports steps that use a rate limit this deployment doesn't
// have: a request on that limit's task queue would never be picked up.
func (s *Server) limitProblems(def flow.Definition) []flow.Problem {
	var problems []flow.Problem
	for _, n := range def.Nodes {
		c, err := flow.DecodeConfig[flow.HTTPConfig](n)
		if err != nil || n.Type != flow.TypeHTTP || c.RateLimit == "" || slices.Contains(s.limitNames(), c.RateLimit) {
			continue
		}
		problems = append(problems, flow.Problem{NodeID: n.ID, Message: fmt.Sprintf("%s uses the rate limit %q, which this deployment doesn't have (RATE_LIMITS)", n.Label, c.RateLimit)})
	}
	return problems
}
