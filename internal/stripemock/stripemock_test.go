package stripemock

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIdempotencyKeysReplayTheFirstAnswer(t *testing.T) {
	Latency = 0
	acct := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv := httptest.NewServer(acct.Handler(""))
	defer srv.Close()
	post := func(key string, form url.Values) (*http.Response, map[string]any) {
		req, _ := http.NewRequest("POST", srv.URL+"/v1/payment_intents", strings.NewReader(form.Encode()))
		req.Header.Set("Authorization", "Bearer "+APIKey)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Idempotency-Key", key)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		var body map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&body)
		return resp, body
	}
	charge := url.Values{"amount": {"2000"}, "currency": {"usd"}, "payment_method": {"pm_card_visa"}, "confirm": {"true"}}

	resp, first := post("k1", charge)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp, again := post("k1", charge)
	require.Equal(t, "true", resp.Header.Get("Idempotent-Replayed"))
	require.Equal(t, first["id"], again["id"])
	require.Len(t, acct.Objects("payment_intent"), 1, "a retried request charges once")

	charge.Set("amount", "3000")
	resp, body := post("k1", charge)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	require.Equal(t, "idempotency_error", body["error"].(map[string]any)["type"])

	charge.Set("payment_method", "pm_card_chargeDeclined")
	resp, body = post("k2", charge)
	require.Equal(t, http.StatusPaymentRequired, resp.StatusCode)
	require.Equal(t, "false", resp.Header.Get("Stripe-Should-Retry"))
	require.Equal(t, "card_declined", body["error"].(map[string]any)["code"])
}
