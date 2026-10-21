package githubmock

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDispatchedWorkflowCallsBack(t *testing.T) {
	Latency, RunDuration = 0, 10*time.Millisecond
	got := make(chan map[string]any, 1)
	callback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		got <- body
	}))
	defer callback.Close()
	srv := httptest.NewServer(New(slog.New(slog.NewTextHandler(io.Discard, nil))).Handler(""))
	defer srv.Close()

	req, _ := http.NewRequest("POST", srv.URL+"/repos/acme/storefront/actions/workflows/deploy.yml/dispatches",
		strings.NewReader(`{"ref": "main", "inputs": {"service": "Storefront", "callback_url": "`+callback.URL+`"}}`))
	req.Header.Set("Authorization", "Bearer "+Token)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	select {
	case body := <-got:
		require.Equal(t, "success", body["conclusion"])
		require.Equal(t, "deploy.yml", body["workflow"])
	case <-time.After(5 * time.Second):
		t.Fatal("the workflow never called back")
	}
}
