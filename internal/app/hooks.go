package app

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"

	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/flow"
	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/workflows"
)

// Hooks are for outside systems. There is no session: the signed flow token
// in the path says whose flow it is, and a run must belong to that flow.

func readJSON(r *http.Request) (any, error) {
	var v any
	err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&v)
	if errors.Is(err, io.EOF) {
		err = nil
	}
	return v, err
}

// hookFlow resolves a token to the flow it names.
func (s *Server) hookFlow(w http.ResponseWriter, r *http.Request) (user, flow.Definition, bool) {
	userKey, flowID, ok := s.parseWebhookToken(r.PathValue("token"))
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("unknown hook"))
		return user{}, flow.Definition{}, false
	}
	u := user{Key: userKey, Name: "webhook"}
	def, ok, err := s.findFlow(r.Context(), u, flowID)
	if err != nil || !ok {
		writeError(w, http.StatusNotFound, errors.New("flow not found"))
		return u, def, false
	}
	return u, def, true
}

// hookRun checks the run in the path belongs to the token's flow. That
// includes its child and scheduled runs, which share the flow's prefix.
func (s *Server) hookRun(w http.ResponseWriter, r *http.Request) (string, bool) {
	userKey, flowID, ok := s.parseWebhookToken(r.PathValue("token"))
	wid := r.PathValue("wid")
	if !ok || !strings.HasPrefix(wid, s.flowRunPrefix(userKey, flowID)) || strings.Contains(wid, "-agent-") {
		writeError(w, http.StatusNotFound, errors.New("unknown run"))
		return "", false
	}
	return wid, true
}

func (s *Server) hookStart(w http.ResponseWriter, r *http.Request) {
	input, err := readJSON(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	u, def, ok := s.hookFlow(w, r)
	if !ok {
		return
	}
	res, err := s.start(r.Context(), r, u, def, input, "webhook")
	switch {
	case err != nil && res.WorkflowID == "":
		writeError(w, http.StatusBadRequest, err)
	case err != nil:
		// Started, but no response: the caller still learns the run's ID.
		writeJSON(w, http.StatusBadGateway, map[string]any{"workflowId": res.WorkflowID, "error": err.Error()})
	default:
		// A flow that responds early answers the webhook's caller with its
		// Respond value, synchronously.
		writeJSON(w, http.StatusOK, res)
	}
}

// hookEvent is a run's callback URL: an outside system POSTs an event and it
// becomes a Signal of that name on the run.
func (s *Server) hookEvent(w http.ResponseWriter, r *http.Request) {
	wid, ok := s.hookRun(w, r)
	if !ok {
		return
	}
	name := r.PathValue("name")
	if !namePattern.MatchString(name) {
		writeError(w, http.StatusBadRequest, errors.New("event name must be lower_snake_case"))
		return
	}
	payload, err := readJSON(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	// Recorded in the run's history even if no worker is running; the run
	// reacts as soon as one runs it.
	if err := s.tc.SignalWorkflow(r.Context(), wid, "", name, payload); err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	s.log.Info("event delivered", "workflow_id", wid, "signal", name)
	w.WriteHeader(http.StatusNoContent)
}

// hookUpdate sends an Update from outside and returns the lane's response:
// a synchronous request to a running Workflow.
func (s *Server) hookUpdate(w http.ResponseWriter, r *http.Request) {
	wid, ok := s.hookRun(w, r)
	if !ok {
		return
	}
	name := r.PathValue("name")
	if !namePattern.MatchString(name) || name == flow.UpdateApprove {
		writeError(w, http.StatusBadRequest, errors.New("unknown update"))
		return
	}
	payload, err := readJSON(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.update(w, r, wid, name, payload)
}

func (s *Server) hookVars(w http.ResponseWriter, r *http.Request) {
	wid, ok := s.hookRun(w, r)
	if !ok {
		return
	}
	s.queryVars(w, r, wid)
}

// hookKeyEvent sends an event to a keyed flow's run for one key, starting
// the run if it isn't running: Signal-With-Start, in one call.
func (s *Server) hookKeyEvent(w http.ResponseWriter, r *http.Request) {
	payload, err := readJSON(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	u, def, ok := s.hookFlow(w, r)
	if !ok {
		return
	}
	name, key := r.PathValue("name"), r.PathValue("key")
	if def.Settings.KeyedBy == "" || !namePattern.MatchString(name) || key == "" {
		writeError(w, http.StatusBadRequest, errors.New("this flow isn't keyed, or the event name is invalid"))
		return
	}
	in, err := s.runInput(r.Context(), r, u, def, payload, "event")
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	in.Key = key
	wid := s.keyedRunID(u.Key, def.ID, key)
	run, err := s.tc.SignalWithStartWorkflow(r.Context(), wid, name, payload, client.StartWorkflowOptions{
		ID: wid, TaskQueue: s.cfg.TaskQueue, Memo: runMemo(in),
		WorkflowIDConflictPolicy: enumspb.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING,
	}, workflows.FlowWorkflow, in)
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"workflowId": run.GetID()})
}
