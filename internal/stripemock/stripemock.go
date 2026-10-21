// Package stripemock is a small, in-memory Stripe account in test mode, for
// the sandbox and the tests. It speaks the parts of the Stripe API the Stripe
// connector uses, with the same paths, form-encoded requests, objects and
// errors:
//
//   - customers: list by email, create, retrieve, delete;
//   - PaymentIntents created and confirmed in one call, with Stripe's test
//     payment methods (pm_card_visa succeeds, pm_card_chargeDeclined and
//     pm_card_chargeDeclinedInsufficientFunds are declined);
//   - refunds, in full or in part;
//   - idempotency keys: a repeated key replays the first answer
//     (Idempotent-Replayed: true), and a key reused with other parameters
//     is refused;
//   - the Stripe-Should-Retry header on errors.
//
// The data lives in memory and is empty on every start.
package stripemock

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// APIKey is the demo account's secret key. The worker uses it by default.
const APIKey = "sk_test_visual_workflows_demo"

// Latency makes calls take about as long as Stripe's.
var Latency = 200 * time.Millisecond

type object = map[string]any

// Account is one in-memory Stripe account.
type Account struct {
	logger *slog.Logger

	mu       sync.Mutex
	objects  map[string]object // ID -> object
	refunded map[string]int64  // PaymentIntent ID -> amount refunded
	replays  map[string]replay // idempotency key -> first answer
}

type replay struct {
	request string
	status  int
	body    []byte
}

// New returns an empty account.
func New(logger *slog.Logger) *Account {
	return &Account{logger: logger, objects: map[string]object{}, refunded: map[string]int64{}, replays: map[string]replay{}}
}

// Handler serves the API. Mount it under a prefix with http.StripPrefix.
func (a *Account) Handler(prefix string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/customers", a.listCustomers)
	mux.HandleFunc("POST /v1/customers", a.createCustomer)
	mux.HandleFunc("GET /v1/customers/{id}", a.retrieve("customer"))
	mux.HandleFunc("DELETE /v1/customers/{id}", a.deleteCustomer)
	mux.HandleFunc("POST /v1/payment_intents", a.createPaymentIntent)
	mux.HandleFunc("GET /v1/payment_intents/{id}", a.retrieve("payment_intent"))
	mux.HandleFunc("POST /v1/refunds", a.createRefund)
	return http.StripPrefix(prefix, a.authenticated(mux))
}

// authenticated checks the secret key (as a bearer token, or as Basic auth's
// user, as Stripe accepts) and applies idempotency keys to POSTs.
func (a *Account) authenticated(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok {
			key, _, _ = r.BasicAuth()
		}
		if key != APIKey {
			writeError(w, http.StatusUnauthorized, "invalid_request_error", "", "Invalid API Key provided: "+redact(key), false)
			return
		}
		time.Sleep(Latency)
		idem := r.Header.Get("Idempotency-Key")
		if r.Method != http.MethodPost || idem == "" {
			next.ServeHTTP(w, r)
			return
		}
		_ = r.ParseForm()
		request := r.URL.Path + "?" + r.PostForm.Encode()
		a.mu.Lock()
		prev, seen := a.replays[idem]
		a.mu.Unlock()
		if seen {
			if prev.request != request {
				writeError(w, http.StatusBadRequest, "idempotency_error", "", fmt.Sprintf("Keys for idempotent requests can only be used with the same parameters they were first used with. Try using a key other than '%s' if you meant to execute a different request.", idem), false)
				return
			}
			a.logger.Info("stripe: replayed an idempotent request", "key", idem)
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Idempotent-Replayed", "true")
			w.WriteHeader(prev.status)
			_, _ = w.Write(prev.body)
			return
		}
		rec := &recorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		// Like Stripe, keep the answer unless the server itself failed.
		if rec.status < 500 {
			a.mu.Lock()
			a.replays[idem] = replay{request: request, status: rec.status, body: rec.body}
			a.mu.Unlock()
		}
	})
}

type recorder struct {
	http.ResponseWriter
	status int
	body   []byte
}

func (r *recorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *recorder) Write(b []byte) (int, error) {
	r.body = append(r.body, b...)
	return r.ResponseWriter.Write(b)
}

func (a *Account) listCustomers(w http.ResponseWriter, r *http.Request) {
	email := r.URL.Query().Get("email")
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit < 1 || limit > 100 {
		limit = 10
	}
	a.mu.Lock()
	var data []object
	for _, o := range a.objects {
		if o["object"] == "customer" && (email == "" || strings.EqualFold(fmt.Sprint(o["email"]), email)) {
			data = append(data, o)
		}
	}
	a.mu.Unlock()
	// Newest first, as Stripe lists.
	slices.SortFunc(data, func(x, y object) int { return int(y["created"].(int64) - x["created"].(int64)) })
	more := len(data) > limit
	data = data[:min(limit, len(data))]
	writeJSON(w, http.StatusOK, object{"object": "list", "url": "/v1/customers", "has_more": more, "data": cmpNil(data)})
}

func (a *Account) createCustomer(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	c := object{
		"id": newID("cus"), "object": "customer", "created": time.Now().Unix(), "livemode": false,
		"email": nullable(r.PostForm.Get("email")), "name": nullable(r.PostForm.Get("name")),
		"description": nullable(r.PostForm.Get("description")), "metadata": metadataOf(r.PostForm),
	}
	a.mu.Lock()
	a.objects[c["id"].(string)] = c
	a.mu.Unlock()
	a.logger.Info("stripe: customer created", "id", c["id"], "email", c["email"])
	writeJSON(w, http.StatusOK, c)
}

func (a *Account) retrieve(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		o, ok := a.objects[r.PathValue("id")]
		a.mu.Unlock()
		if !ok || o["object"] != kind {
			writeMissing(w, kind, r.PathValue("id"))
			return
		}
		writeJSON(w, http.StatusOK, o)
	}
}

func (a *Account) deleteCustomer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	a.mu.Lock()
	o, ok := a.objects[id]
	if ok && o["object"] == "customer" {
		delete(a.objects, id)
	}
	a.mu.Unlock()
	if !ok || o["object"] != "customer" {
		writeMissing(w, "customer", id)
		return
	}
	a.logger.Info("stripe: customer deleted", "id", id)
	writeJSON(w, http.StatusOK, object{"id": id, "object": "customer", "deleted": true})
}

// Test payment methods and what happens when they are charged: "" succeeds,
// anything else is the decline code.
var paymentMethods = map[string]string{
	"pm_card_visa": "", "pm_card_mastercard": "", "pm_card_amex": "",
	"pm_card_chargeDeclined":                  "generic_decline",
	"pm_card_chargeDeclinedInsufficientFunds": "insufficient_funds",
	"pm_card_chargeDeclinedExpiredCard":       "expired_card",
}

func (a *Account) createPaymentIntent(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	f := r.PostForm
	amount, err := strconv.ParseInt(f.Get("amount"), 10, 64)
	switch {
	case f.Get("amount") == "":
		writeError(w, http.StatusBadRequest, "invalid_request_error", "parameter_missing", "Missing required param: amount.", false)
		return
	case err != nil:
		writeError(w, http.StatusBadRequest, "invalid_request_error", "parameter_invalid_integer", "Invalid integer: "+f.Get("amount"), false)
		return
	case amount < 50:
		writeError(w, http.StatusBadRequest, "invalid_request_error", "amount_too_small", "Amount must be at least $0.50 usd", false)
		return
	case f.Get("currency") == "":
		writeError(w, http.StatusBadRequest, "invalid_request_error", "parameter_missing", "Missing required param: currency.", false)
		return
	}
	customer := f.Get("customer")
	a.mu.Lock()
	c, ok := a.objects[customer]
	a.mu.Unlock()
	if customer != "" && (!ok || c["object"] != "customer") {
		writeMissing(w, "customer", customer)
		return
	}
	pm := f.Get("payment_method")
	decline, known := paymentMethods[pm]
	if pm != "" && !known {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "resource_missing", fmt.Sprintf("No such PaymentMethod: '%s'", pm), false)
		return
	}
	pi := object{
		"id": newID("pi"), "object": "payment_intent", "created": time.Now().Unix(), "livemode": false,
		"amount": amount, "amount_received": int64(0), "currency": strings.ToLower(f.Get("currency")),
		"customer": nullable(customer), "description": nullable(f.Get("description")),
		"metadata": metadataOf(f), "payment_method": nullable(pm), "latest_charge": nil,
		"status": "requires_payment_method",
	}
	if pm != "" && f.Get("confirm") == "true" {
		if decline != "" {
			pi["last_payment_error"] = object{"code": "card_declined", "decline_code": decline}
			a.store(pi)
			a.logger.Warn("stripe: card declined", "payment_intent", pi["id"], "decline_code", decline)
			writeJSON(w, http.StatusPaymentRequired, object{"error": object{
				"type": "card_error", "code": "card_declined", "decline_code": decline,
				"message": declineMessages[decline], "payment_intent": pi,
			}}, "false")
			return
		}
		pi["status"], pi["amount_received"], pi["latest_charge"] = "succeeded", amount, newID("ch")
	} else if pm != "" {
		pi["status"] = "requires_confirmation"
	}
	a.store(pi)
	a.logger.Info("stripe: payment intent", "id", pi["id"], "amount", amount, "status", pi["status"])
	writeJSON(w, http.StatusOK, pi)
}

var declineMessages = map[string]string{
	"generic_decline":    "Your card was declined.",
	"insufficient_funds": "Your card has insufficient funds.",
	"expired_card":       "Your card has expired.",
}

func (a *Account) createRefund(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	f := r.PostForm
	id := f.Get("payment_intent")
	a.mu.Lock()
	defer a.mu.Unlock()
	pi, ok := a.objects[id]
	if id == "" || !ok || pi["object"] != "payment_intent" {
		writeMissing(w, "payment_intent", id)
		return
	}
	if pi["status"] != "succeeded" {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "charge_not_refundable", "This PaymentIntent has no successful charge to refund.", false)
		return
	}
	left := pi["amount_received"].(int64) - a.refunded[id]
	amount := left
	if s := f.Get("amount"); s != "" {
		amount, _ = strconv.ParseInt(s, 10, 64)
	}
	switch {
	case left == 0:
		writeError(w, http.StatusBadRequest, "invalid_request_error", "charge_already_refunded", fmt.Sprintf("Charge %s has already been refunded.", pi["latest_charge"]), false)
		return
	case amount < 1 || amount > left:
		writeError(w, http.StatusBadRequest, "invalid_request_error", "amount_too_large", fmt.Sprintf("Refund amount ($%.2f) is greater than unrefunded amount on charge ($%.2f)", float64(amount)/100, float64(left)/100), false)
		return
	}
	a.refunded[id] += amount
	re := object{
		"id": newID("re"), "object": "refund", "created": time.Now().Unix(), "amount": amount,
		"currency": pi["currency"], "payment_intent": id, "charge": pi["latest_charge"],
		"reason": nullable(f.Get("reason")), "status": "succeeded",
	}
	a.objects[re["id"].(string)] = re
	a.logger.Info("stripe: refund", "id", re["id"], "payment_intent", id, "amount", amount)
	writeJSON(w, http.StatusOK, re)
}

func (a *Account) store(o object) {
	a.mu.Lock()
	a.objects[o["id"].(string)] = o
	a.mu.Unlock()
}

// Objects returns copies of the account's objects of a kind, for tests.
func (a *Account) Objects(kind string) []map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []map[string]any
	for _, o := range a.objects {
		if o["object"] == kind {
			c := map[string]any{}
			for k, v := range o {
				c[k] = v
			}
			out = append(out, c)
		}
	}
	return out
}

// Refunded reports how much of a PaymentIntent has been refunded.
func (a *Account) Refunded(id string) int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.refunded[id]
}

func metadataOf(f url.Values) object {
	m := object{}
	for k, v := range f {
		if name, ok := strings.CutPrefix(k, "metadata["); ok && strings.HasSuffix(name, "]") {
			m[strings.TrimSuffix(name, "]")] = v[0]
		}
	}
	return m
}

func writeMissing(w http.ResponseWriter, kind, id string) {
	writeError(w, http.StatusNotFound, "invalid_request_error", "resource_missing", fmt.Sprintf("No such %s: '%s'", kind, id), false)
}

func writeError(w http.ResponseWriter, status int, typ, code, msg string, retry bool) {
	e := object{"type": typ, "message": msg}
	if code != "" {
		e["code"] = code
	}
	writeJSON(w, status, object{"error": e}, strconv.FormatBool(retry))
}

func writeJSON(w http.ResponseWriter, status int, v any, shouldRetry ...string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Request-Id", newID("req"))
	if len(shouldRetry) > 0 {
		w.Header().Set("Stripe-Should-Retry", shouldRetry[0])
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

const base62 = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

func newID(prefix string) string {
	b := make([]byte, 14)
	for i := range b {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(base62))))
		b[i] = base62[n.Int64()]
	}
	return prefix + "_" + string(b)
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func cmpNil(data []object) []object {
	if data == nil {
		return []object{}
	}
	return data
}

func redact(key string) string {
	if len(key) <= 12 {
		return strings.Repeat("*", len(key))
	}
	return key[:7] + strings.Repeat("*", len(key)-11) + key[len(key)-4:]
}
