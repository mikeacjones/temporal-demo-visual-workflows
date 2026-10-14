package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/temporal-sa/temporal-demo-visual-workflows/internal/flow"
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
		writeError(w, startErrorStatus(err), err)
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
	if !namePattern.MatchString(name) || key == "" {
		writeError(w, http.StatusBadRequest, errors.New("the event name is invalid"))
		return
	}
	wid, err := s.signalWithStart(r.Context(), r, u, def, key, name, payload, "event")
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"workflowId": wid})
}

// hookKeyUpdate sends an Update to a keyed flow's run for one key, starting
// the run if its conflict policy allows: Update-With-Start, in one call. The
// response is the On update lane's Respond value.
func (s *Server) hookKeyUpdate(w http.ResponseWriter, r *http.Request) {
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
	if !namePattern.MatchString(name) || key == "" {
		writeError(w, http.StatusBadRequest, errors.New("the update name is invalid"))
		return
	}
	res, err := s.updateWithStart(r.Context(), r, u, def, key, name, payload, "update")
	writeWithStartResult(w, res, err)
}

// writeWithStartResult answers an Update-With-Start: the lane's response, or
// why there isn't one.
func writeWithStartResult(w http.ResponseWriter, res started, err error) {
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]any{"workflowId": res.WorkflowID, "result": res.Response})
	case errors.Is(err, context.DeadlineExceeded):
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"workflowId": res.WorkflowID, "error": "no worker answered in time; the run may have started without handling the update"})
	default:
		writeError(w, http.StatusConflict, err)
	}
}
