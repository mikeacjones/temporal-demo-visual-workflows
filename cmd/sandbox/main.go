// Command sandbox simulates the outside world that demo flows talk to: a small
// store with orders, customers, support tickets, refunds, notifications, and a
// deliberately unreliable endpoint. It stands in for real third-party APIs so
// every demo beat is repeatable.
//
// Like a real store or payment provider, it calls back instead of making
// callers poll: an order created with a callbackUrl gets a POST on every status
// change, and a successful charge gets a settlement callback. GET /orders/{id}
// still answers (the order ID encodes its creation time and the status follows
// its age), so lookups survive a sandbox restart; pending callbacks don't.
package main

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	addr := envOr("SANDBOX_ADDR", ":8090")
	flaky := &flakyCounter{seen: map[string]*flakyEntry{}}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) })

	mux.HandleFunc("POST /orders", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Item        string  `json:"item"`
			Customer    string  `json:"customer"`
			Amount      float64 `json:"amount"`
			CallbackURL string  `json:"callbackUrl"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		created := time.Now()
		id := fmt.Sprintf("ord_%s_%s", strconv.FormatInt(created.UnixMilli(), 36), randomID(4))
		logger.Info("order created", "order", id, "item", in.Item, "callback", in.CallbackURL != "")
		if in.CallbackURL != "" {
			for _, step := range orderTimeline[1:] {
				time.AfterFunc(step.after, func() {
					callback(logger, in.CallbackURL, map[string]any{"id": id, "status": step.status, "at": time.Now().UTC()})
				})
			}
		}
		writeJSON(w, http.StatusCreated, order(id, created))
	})

	mux.HandleFunc("GET /orders/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		created, ok := orderCreatedAt(id)
		if !ok {
			// Fixed demo orders (e.g. A-1001) behave as if placed a minute ago.
			created = time.Now().Add(-time.Minute)
		}
		writeJSON(w, http.StatusOK, order(id, created))
	})

	mux.HandleFunc("GET /customers/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		h := hash(id)
		tiers := []string{"bronze", "silver", "gold", "platinum"}
		names := []string{"Ada Park", "Ben Okafor", "Chloe Martin", "Diego Ruiz", "Emi Tanaka", "Farah Haddad"}
		writeJSON(w, http.StatusOK, map[string]any{
			"id":            id,
			"name":          names[h%uint32(len(names))],
			"tier":          tiers[(h/7)%uint32(len(tiers))],
			"lifetimeValue": 200 + int(h%4800),
			"ordersLast90d": 1 + int(h%9),
		})
	})

	mux.HandleFunc("GET /tickets/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		tickets := []map[string]any{
			{"subject": "Blender arrived cracked", "body": "My order A-1001 arrived today and the blender jar is cracked down the side. I need this for a party on Saturday. Can you help?", "customerId": "C-42", "orderId": "A-1001", "sku": "BLND-500"},
			{"subject": "Where is my order?", "body": "Order A-2002 was supposed to arrive yesterday. Tracking hasn't changed in three days.", "customerId": "C-77", "orderId": "A-2002", "sku": "KTL-200"},
			{"subject": "Wrong colour", "body": "I ordered the kettle in blue (order A-3003) but received red. Not a huge deal but I'd prefer blue.", "customerId": "C-13", "orderId": "A-3003", "sku": "KTL-200"},
		}
		t := tickets[hash(id)%uint32(len(tickets))]
		t["id"] = id
		writeJSON(w, http.StatusOK, t)
	})

	mux.HandleFunc("GET /inventory/{sku}", func(w http.ResponseWriter, r *http.Request) {
		sku := r.PathValue("sku")
		writeJSON(w, http.StatusOK, map[string]any{"sku": sku, "inStock": int(hash(sku) % 40), "warehouse": "PDX-2"})
	})

	mux.HandleFunc("POST /refunds", func(w http.ResponseWriter, r *http.Request) {
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		// Honor an idempotency key so a retried refund is not issued twice.
		key := r.Header.Get("Idempotency-Key")
		if key == "" {
			key = randomID(8)
		}
		logger.Info("refund issued", "order", in["orderId"], "amount", in["amount"], "idempotency_key", key)
		writeJSON(w, http.StatusOK, map[string]any{"refundId": "rf_" + strconv.FormatUint(uint64(hash(key)), 36), "status": "issued", "request": in})
	})

	mux.HandleFunc("POST /notify", func(w http.ResponseWriter, r *http.Request) {
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		logger.Info("notification sent", "channel", in["channel"], "text", in["text"])
		writeJSON(w, http.StatusOK, map[string]any{"delivered": true, "channel": in["channel"], "at": time.Now().UTC()})
	})

	// Bookings for the saga example: create, and cancel (the undo).
	mux.HandleFunc("POST /bookings/{kind}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("kind")[:1] + "_" + randomID(4)
		logger.Info("booking confirmed", "kind", r.PathValue("kind"), "booking", id)
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "kind": r.PathValue("kind"), "status": "confirmed"})
	})
	mux.HandleFunc("POST /bookings/{kind}/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		logger.Info("booking cancelled", "kind", r.PathValue("kind"), "booking", r.PathValue("id"))
		writeJSON(w, http.StatusOK, map[string]any{"id": r.PathValue("id"), "status": "cancelled"})
	})
	// A card processor that declines on request. A decline is a business
	// answer (402), so it is not retried.
	mux.HandleFunc("POST /cards/charge", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("decline") == "true" {
			logger.Warn("card declined")
			writeJSON(w, http.StatusPaymentRequired, map[string]any{"error": "card declined"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"chargeId": "ch_" + randomID(4)})
	})

	// Carrier quotes for the race example. Swift answers in a few seconds,
	// Parcel Post takes half a minute, and Budget Freight is down (a 503,
	// which the step retries until its policy gives up).
	mux.HandleFunc("POST /quotes/{carrier}", func(w http.ResponseWriter, r *http.Request) {
		carrier := r.PathValue("carrier")
		quote, ok := carrierQuotes[carrier]
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "unknown carrier " + carrier})
			return
		}
		if quote.down {
			logger.Warn("carrier down on purpose", "carrier", carrier)
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": carrier + " is not answering"})
			return
		}
		// Read the body first: Go only notices a caller hanging up (and
		// cancels r.Context()) once the request body has been consumed.
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-time.After(quote.after):
		case <-r.Context().Done():
			logger.Info("quote request abandoned by the caller", "carrier", carrier)
			return
		}
		logger.Info("quote sent", "carrier", carrier, "price", quote.price)
		writeJSON(w, http.StatusOK, map[string]any{"carrier": carrier, "price": quote.price, "days": quote.days, "quoteId": "q_" + randomID(4)})
	})

	// Paged records for the batch iterator: five pages of five, linked by an
	// opaque cursor.
	mux.HandleFunc("GET /records", func(w http.ResponseWriter, r *http.Request) {
		page, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Query().Get("cursor"), "page-"))
		items := make([]map[string]any, 0, 5)
		for i := range 5 {
			n := page*5 + i + 1
			items = append(items, map[string]any{"id": fmt.Sprintf("R-%03d", n), "amount": 10 + n})
		}
		next := ""
		if page < 4 {
			next = fmt.Sprintf("page-%d", page+1)
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items, "next": next, "page": page + 1})
	})

	// /ratelimited/{key}?times=N answers 429 with Retry-After for the first N
	// calls per key, like an API that tells callers when to come back.
	mux.HandleFunc("/ratelimited/{key}", func(w http.ResponseWriter, r *http.Request) {
		times, _ := strconv.Atoi(r.URL.Query().Get("times"))
		after := r.URL.Query().Get("after")
		if after == "" {
			after = "8"
		}
		if attempt := flaky.next("rl-" + r.PathValue("key")); attempt <= times {
			logger.Warn("rate limited on purpose", "key", r.PathValue("key"), "attempt", attempt, "retryAfter", after)
			w.Header().Set("Retry-After", after)
			writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "slow down", "retryAfterSeconds": after})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	// Address validation rejects a ZIP that isn't five digits. The 422 is a
	// bad request, so retrying it unchanged can never succeed.
	mux.HandleFunc("POST /addresses/validate", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Street string `json:"street"`
			Zip    string `json:"zip"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		if len(in.Zip) != 5 || strings.Trim(in.Zip, "0123456789") != "" {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": fmt.Sprintf("zip %q is not a 5-digit ZIP code", in.Zip)})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"valid": true, "street": in.Street, "zip": in.Zip, "zone": "Z" + in.Zip[:1]})
	})

	// A slow job, for long-running work: answers after ?seconds=N (max 120),
	// or gives up as soon as the caller hangs up.
	mux.HandleFunc("POST /jobs/slow", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		secs, _ := strconv.Atoi(r.URL.Query().Get("seconds"))
		secs = min(max(secs, 1), 120)
		select {
		case <-time.After(time.Duration(secs) * time.Second):
			writeJSON(w, http.StatusOK, map[string]any{"done": true, "seconds": secs, "report": "rep_" + randomID(4)})
		case <-r.Context().Done():
			logger.Info("slow job abandoned by the caller", "seconds", secs)
		}
	})

	// /flaky/{key}?failures=N returns 503 for the first N calls per key, then
	// succeeds. Use a per-run key ({{ run.id }}) so every run sees the same
	// failures. The counter lives in memory: this is the simulated unreliable
	// third party, not part of the demo's own state.
	mux.HandleFunc("/flaky/{key}", func(w http.ResponseWriter, r *http.Request) {
		failures, _ := strconv.Atoi(r.URL.Query().Get("failures"))
		key := r.PathValue("key")
		attempt := flaky.next(key)
		if attempt <= failures {
			logger.Warn("flaky endpoint failing on purpose", "key", key, "attempt", attempt, "failures", failures)
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "payment gateway unavailable", "attempt": attempt})
			return
		}
		var in struct {
			CallbackURL string `json:"callbackUrl"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		tx := "tx_" + randomID(6)
		if in.CallbackURL != "" {
			// The bank settles a little later and says so with a callback.
			time.AfterFunc(8*time.Second, func() {
				callback(logger, in.CallbackURL, map[string]any{"transactionId": tx, "bank": "ACME Bank", "reference": "S-" + randomID(3), "settledAt": time.Now().UTC()})
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "attempt": attempt, "transactionId": tx})
	})

	logger.Info("sandbox listening", "addr", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		logger.Error("sandbox stopped", "error", err)
		os.Exit(1)
	}
}

var carrierQuotes = map[string]struct {
	after time.Duration
	price float64
	days  int
	down  bool
}{
	"swift":       {after: 4 * time.Second, price: 18.40, days: 2},
	"parcel-post": {after: 30 * time.Second, price: 11.90, days: 5},
	"budget":      {down: true},
}

// Order statuses advance with age, at a pace that suits a live demo.
var orderTimeline = []struct {
	after  time.Duration
	status string
}{
	{0, "created"}, {5 * time.Second, "paid"}, {12 * time.Second, "packed"},
	{25 * time.Second, "shipped"}, {60 * time.Second, "delivered"},
}

func order(id string, created time.Time) map[string]any {
	age := time.Since(created)
	status := "created"
	for _, step := range orderTimeline {
		if age >= step.after {
			status = step.status
		}
	}
	return map[string]any{"id": id, "status": status, "createdAt": created.UTC(), "total": 40 + int(hash(id)%260)}
}

func orderCreatedAt(id string) (time.Time, bool) {
	parts := strings.Split(id, "_")
	if len(parts) != 3 || parts[0] != "ord" {
		return time.Time{}, false
	}
	ms, err := strconv.ParseInt(parts[1], 36, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.UnixMilli(ms), true
}

type flakyEntry struct {
	count int
	last  time.Time
}

type flakyCounter struct {
	mu   sync.Mutex
	seen map[string]*flakyEntry
}

func (f *flakyCounter) next(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	for k, e := range f.seen {
		if now.Sub(e.last) > 30*time.Minute {
			delete(f.seen, k)
		}
	}
	e := f.seen[key]
	if e == nil {
		e = &flakyEntry{}
		f.seen[key] = e
	}
	e.count++
	e.last = now
	return e.count
}

// callback POSTs an event to a caller-supplied URL, retrying briefly the way
// webhook senders do.
func callback(logger *slog.Logger, url string, body map[string]any) {
	raw, _ := json.Marshal(body)
	for attempt := 1; attempt <= 5; attempt++ {
		resp, err := http.Post(url, "application/json", bytes.NewReader(raw))
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 300 {
				logger.Info("callback delivered", "url", url, "event", string(raw))
				return
			}
			err = fmt.Errorf("status %s", resp.Status)
		}
		logger.Warn("callback failed; retrying", "url", url, "attempt", attempt, "error", err)
		time.Sleep(time.Duration(attempt) * time.Second)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func hash(s string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return h.Sum32()
}

func randomID(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x", b)
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
