// Package salesforcemock is a small, in-memory Salesforce org for the sandbox
// and the tests. It speaks the parts of the REST API the Salesforce connector
// uses, with the same paths, payloads and error codes, so the connector's
// Activities can't tell it from a real org:
//
//   - OAuth 2.0 client credentials at /services/oauth2/token, and bearer
//     tokens that expire (and are forgotten when the sandbox restarts).
//   - SOQL queries: SELECT fields (including Account.Name) or COUNT() FROM
//     one object, WHERE with AND/OR, =, !=, <, >, LIKE, IN, and date
//     literals (TODAY, LAST_N_DAYS:n), ORDER BY, LIMIT and OFFSET, paged
//     through nextRecordsUrl.
//   - sObject rows: get, create, update, upsert by an external ID field, and
//     delete, with required fields and a validation rule.
//   - Row locks. A write holds its record, and the parent Account of a record
//     with an AccountId, for a moment. Another write to a held row fails at
//     once with UNABLE_TO_LOCK_ROW (a real org waits up to 10 seconds first),
//     so parallel writes under one Account show Temporal retrying.
//
// The data lives in memory and is seeded fresh on every start.
package salesforcemock

import (
	"cmp"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The demo connected app's credentials. The worker uses these by default.
const (
	ClientID     = "visual-workflows-demo"
	ClientSecret = "demo-secret"
)

// Latency makes calls take about as long as a real org's. A write holds its
// rows for WriteLatency, which is what lets parallel writes collide.
var (
	ReadLatency  = 150 * time.Millisecond
	WriteLatency = 600 * time.Millisecond
)

const (
	tokenLifetime = 2 * time.Hour
	batchSize     = 200
)

// Key prefixes, the first three characters of every record ID.
var prefixes = map[string]string{
	"Account": "001", "Contact": "003", "Opportunity": "006", "Lead": "00Q", "Case": "500", "Task": "00T", "User": "005",
}

// required lists fields a create must set, as a real org's schema does.
var required = map[string][]string{
	"Account":     {"Name"},
	"Contact":     {"LastName"},
	"Lead":        {"LastName", "Company"},
	"Opportunity": {"Name", "StageName", "CloseDate"},
}

type record = map[string]any

// Org is one in-memory org.
type Org struct {
	logger *slog.Logger
	now    func() time.Time

	mu      sync.Mutex
	records map[string]map[string]record // object -> ID -> fields
	counter int
	tokens  map[string]time.Time // access token -> expiry
	cursors map[string][]record  // query locator -> remaining rows
	locks   map[string]time.Time // record ID -> held until
}

// New returns an org seeded with demo data.
func New(logger *slog.Logger) *Org {
	o := &Org{
		logger: logger, now: time.Now,
		records: map[string]map[string]record{}, tokens: map[string]time.Time{},
		cursors: map[string][]record{}, locks: map[string]time.Time{},
	}
	o.seed()
	return o
}

// Handler serves the org's API. Mount it under a prefix with
// http.StripPrefix; the token response's instance_url includes the prefix.
func (o *Org) Handler(prefix string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /services/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		o.token(w, r, prefix)
	})
	api := func(pattern string, h func(http.ResponseWriter, *http.Request)) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			if !o.authorized(r) {
				writeErrors(w, http.StatusUnauthorized, "INVALID_SESSION_ID", "Session expired or invalid")
				return
			}
			h(w, r)
		})
	}
	api("GET /services/data/{version}/query", o.query)
	api("GET /services/data/{version}/query/{locator}", o.queryMore)
	api("GET /services/data/{version}/sobjects/{object}/{id}", o.get)
	api("POST /services/data/{version}/sobjects/{object}", o.create)
	api("PATCH /services/data/{version}/sobjects/{object}/{id}", o.update)
	api("PATCH /services/data/{version}/sobjects/{object}/{field}/{value}", o.upsert)
	api("DELETE /services/data/{version}/sobjects/{object}/{id}", o.delete)
	return http.StripPrefix(prefix, mux)
}

func (o *Org) token(w http.ResponseWriter, r *http.Request, prefix string) {
	_ = r.ParseForm()
	if r.PostForm.Get("grant_type") != "client_credentials" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "unsupported_grant_type", "error_description": "grant type not supported"})
		return
	}
	id, secret := r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	if u, p, ok := r.BasicAuth(); ok {
		id, secret = u, p
	}
	if id != ClientID || secret != ClientSecret {
		o.logger.Warn("salesforce: rejected client credentials", "client_id", id)
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_client", "error_description": "invalid client credentials"})
		return
	}
	tok := "00DHs0000demo!" + randomHex(24)
	o.mu.Lock()
	for t, exp := range o.tokens {
		if o.now().After(exp) {
			delete(o.tokens, t)
		}
	}
	o.tokens[tok] = o.now().Add(tokenLifetime)
	o.mu.Unlock()
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	o.logger.Info("salesforce: issued an access token", "client_id", id)
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": tok, "instance_url": scheme + "://" + r.Host + prefix,
		"token_type": "Bearer", "scope": "api", "issued_at": strconv.FormatInt(o.now().UnixMilli(), 10),
	})
}

func (o *Org) authorized(r *http.Request) bool {
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return false
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	exp, ok := o.tokens[tok]
	return ok && o.now().Before(exp)
}

func (o *Org) query(w http.ResponseWriter, r *http.Request) {
	time.Sleep(ReadLatency)
	q, err := parseSOQL(r.URL.Query().Get("q"))
	if err != nil {
		writeErrors(w, http.StatusBadRequest, "MALFORMED_QUERY", err.Error())
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if _, ok := o.records[q.object]; !ok {
		writeErrors(w, http.StatusBadRequest, "INVALID_TYPE", fmt.Sprintf("sObject type '%s' is not supported.", q.object))
		return
	}
	rows, err := q.run(o.records[q.object], o.lookup, o.now())
	if err != nil {
		writeErrors(w, http.StatusBadRequest, "MALFORMED_QUERY", err.Error())
		return
	}
	o.logger.Info("salesforce: query", "soql", r.URL.Query().Get("q"), "rows", len(rows))
	if q.count {
		writeJSON(w, http.StatusOK, map[string]any{"totalSize": len(rows), "done": true, "records": []any{}})
		return
	}
	o.page(w, r, len(rows), rows)
}

func (o *Org) queryMore(w http.ResponseWriter, r *http.Request) {
	time.Sleep(ReadLatency)
	o.mu.Lock()
	defer o.mu.Unlock()
	locator := r.PathValue("locator")
	rows, ok := o.cursors[locator]
	if !ok {
		writeErrors(w, http.StatusBadRequest, "INVALID_QUERY_LOCATOR", "invalid query locator")
		return
	}
	delete(o.cursors, locator)
	total, _ := strconv.Atoi(locator[strings.LastIndex(locator, "-")+1:])
	o.page(w, r, total, rows)
}

// page answers with one batch of rows and keeps the rest behind a locator.
// Callers hold o.mu.
func (o *Org) page(w http.ResponseWriter, r *http.Request, total int, rows []record) {
	out := map[string]any{"totalSize": total, "done": true}
	if len(rows) > batchSize {
		locator := fmt.Sprintf("01g%s-%d", randomHex(6), total)
		o.cursors[locator] = rows[batchSize:]
		rows = rows[:batchSize]
		out["done"] = false
		out["nextRecordsUrl"] = fmt.Sprintf("/services/data/%s/query/%s", r.PathValue("version"), locator)
	}
	out["records"] = rows
	writeJSON(w, http.StatusOK, out)
}

func (o *Org) get(w http.ResponseWriter, r *http.Request) {
	time.Sleep(ReadLatency)
	object, id := canonicalObject(r.PathValue("object")), r.PathValue("id")
	o.mu.Lock()
	defer o.mu.Unlock()
	rec, status, code, msg := o.find(object, id)
	if rec == nil {
		writeErrors(w, status, code, msg)
		return
	}
	var fields []string
	if f := r.URL.Query().Get("fields"); f != "" {
		for _, name := range strings.Split(f, ",") {
			fields = append(fields, strings.TrimSpace(name))
		}
	}
	writeJSON(w, http.StatusOK, o.view(object, rec, fields))
}

func (o *Org) create(w http.ResponseWriter, r *http.Request) {
	object := canonicalObject(r.PathValue("object"))
	values, ok := readValues(w, r)
	if !ok {
		return
	}
	if !o.writable(w, object) {
		return
	}
	if code, msg, fields := validate(object, values, true); code != "" {
		writeFieldErrors(w, code, msg, fields)
		return
	}
	release, ok := o.lock(w, "", values)
	if !ok {
		return
	}
	defer release()
	time.Sleep(WriteLatency)
	o.mu.Lock()
	id := o.insert(object, values)
	o.mu.Unlock()
	o.logger.Info("salesforce: record created", "object", object, "id", id)
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "success": true, "errors": []any{}})
}

func (o *Org) update(w http.ResponseWriter, r *http.Request) {
	object, id := canonicalObject(r.PathValue("object")), r.PathValue("id")
	values, ok := readValues(w, r)
	if !ok {
		return
	}
	o.mu.Lock()
	rec, status, code, msg := o.find(object, id)
	o.mu.Unlock()
	if rec == nil {
		writeErrors(w, status, code, msg)
		return
	}
	if code, msg, fields := validate(object, values, false); code != "" {
		writeFieldErrors(w, code, msg, fields)
		return
	}
	release, ok := o.lock(w, id, merged(rec, values))
	if !ok {
		return
	}
	defer release()
	time.Sleep(WriteLatency)
	o.mu.Lock()
	o.apply(rec, values)
	o.mu.Unlock()
	o.logger.Info("salesforce: record updated", "object", object, "id", id, "fields", slices.Sorted(maps.Keys(values)))
	w.WriteHeader(http.StatusNoContent)
}

// upsert updates the record whose external ID field has the value, or creates
// one with it. Like a real org, it answers 201 for a create and 200 for an
// update, and 300 when the value matches more than one record.
func (o *Org) upsert(w http.ResponseWriter, r *http.Request) {
	object, field, value := canonicalObject(r.PathValue("object")), r.PathValue("field"), r.PathValue("value")
	values, ok := readValues(w, r)
	if !ok {
		return
	}
	if !o.writable(w, object) {
		return
	}
	o.mu.Lock()
	var matches []record
	for _, rec := range o.records[object] {
		if fmt.Sprint(rec[field]) == value {
			matches = append(matches, rec)
		}
	}
	o.mu.Unlock()
	switch len(matches) {
	case 0:
		values[field] = value
		if code, msg, fields := validate(object, values, true); code != "" {
			writeFieldErrors(w, code, msg, fields)
			return
		}
		release, ok := o.lock(w, "", values)
		if !ok {
			return
		}
		defer release()
		time.Sleep(WriteLatency)
		o.mu.Lock()
		id := o.insert(object, values)
		o.mu.Unlock()
		o.logger.Info("salesforce: upsert created a record", "object", object, "id", id, field, value)
		writeJSON(w, http.StatusCreated, map[string]any{"id": id, "success": true, "errors": []any{}, "created": true})
	case 1:
		rec := matches[0]
		id := rec["Id"].(string)
		if code, msg, fields := validate(object, values, false); code != "" {
			writeFieldErrors(w, code, msg, fields)
			return
		}
		release, ok := o.lock(w, id, merged(rec, values))
		if !ok {
			return
		}
		defer release()
		time.Sleep(WriteLatency)
		o.mu.Lock()
		o.apply(rec, values)
		o.mu.Unlock()
		o.logger.Info("salesforce: upsert updated a record", "object", object, "id", id, field, value)
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "success": true, "errors": []any{}, "created": false})
	default:
		urls := []string{}
		for _, rec := range matches {
			urls = append(urls, fmt.Sprintf("/services/data/%s/sobjects/%s/%s", r.PathValue("version"), object, rec["Id"]))
		}
		writeJSON(w, http.StatusMultipleChoices, urls)
	}
}

func (o *Org) delete(w http.ResponseWriter, r *http.Request) {
	object, id := canonicalObject(r.PathValue("object")), r.PathValue("id")
	o.mu.Lock()
	rec, status, code, msg := o.find(object, id)
	o.mu.Unlock()
	if rec == nil {
		writeErrors(w, status, code, msg)
		return
	}
	release, ok := o.lock(w, id, rec)
	if !ok {
		return
	}
	defer release()
	time.Sleep(WriteLatency)
	o.mu.Lock()
	delete(o.records[object], id)
	o.mu.Unlock()
	o.logger.Info("salesforce: record deleted", "object", object, "id", id)
	w.WriteHeader(http.StatusNoContent)
}

// lock holds a record (when id is set) and the parent Account it points to
// for the length of a write. If either is already held it answers
// UNABLE_TO_LOCK_ROW and reports false.
func (o *Org) lock(w http.ResponseWriter, id string, rec record) (release func(), ok bool) {
	rows := []string{}
	if id != "" {
		rows = append(rows, id)
	}
	if parent, _ := rec["AccountId"].(string); parent != "" && parent != id {
		rows = append(rows, parent)
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	now := o.now()
	for _, row := range rows {
		if until, held := o.locks[row]; held && now.Before(until) {
			o.logger.Warn("salesforce: row locked by another write", "row", row)
			writeErrors(w, http.StatusBadRequest, "UNABLE_TO_LOCK_ROW", "unable to obtain exclusive access to this record or 1 records: "+row)
			return nil, false
		}
	}
	for _, row := range rows {
		o.locks[row] = now.Add(WriteLatency + time.Second)
	}
	return func() {
		o.mu.Lock()
		defer o.mu.Unlock()
		for _, row := range rows {
			delete(o.locks, row)
		}
	}, true
}

func (o *Org) writable(w http.ResponseWriter, object string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	if _, ok := o.records[object]; ok {
		return true
	}
	if strings.HasSuffix(object, "__c") {
		o.records[object] = map[string]record{}
		return true
	}
	writeErrors(w, http.StatusNotFound, "NOT_FOUND", "The requested resource does not exist")
	return false
}

// find returns the record, or the status and error to answer with. Callers
// hold o.mu.
func (o *Org) find(object, id string) (record, int, string, string) {
	rows, ok := o.records[object]
	if !ok {
		return nil, http.StatusNotFound, "NOT_FOUND", "The requested resource does not exist"
	}
	if rec, ok := rows[id]; ok {
		return rec, 0, "", ""
	}
	if len(id) != 15 && len(id) != 18 {
		return nil, http.StatusBadRequest, "MALFORMED_ID", fmt.Sprintf("%s ID: id value of incorrect type: %s", object, id)
	}
	return nil, http.StatusNotFound, "NOT_FOUND", "The requested resource does not exist"
}

// insert stores a new record and returns its ID. Callers hold o.mu.
func (o *Org) insert(object string, values record) string {
	o.counter++
	prefix := cmp.Or(prefixes[object], "a0X")
	n := strconv.FormatInt(int64(o.counter), 36)
	id := withChecksum(prefix + "Hs" + strings.Repeat("0", 10-len(n)) + n)
	now := o.now().UTC().Format(timeFormat)
	rec := record{"Id": id, "CreatedDate": now, "LastModifiedDate": now}
	maps.Copy(rec, values)
	o.records[object][id] = rec
	return id
}

func (o *Org) apply(rec, values record) {
	maps.Copy(rec, values)
	rec["LastModifiedDate"] = o.now().UTC().Format(timeFormat)
}

// lookup finds a record by ID in any object, for relationship fields such as
// Account.Name. Callers hold o.mu.
func (o *Org) lookup(id string) (string, record) {
	for object, rows := range o.records {
		if rec, ok := rows[id]; ok {
			return object, rec
		}
	}
	return "", nil
}

// view is a record as the API returns it: the requested fields (all when
// none are named) with attributes, and the derived IsClosed flags.
func (o *Org) view(object string, rec record, fields []string) record {
	full := derived(object, rec)
	out := record{"attributes": attributes(object, rec["Id"])}
	if len(fields) == 0 {
		maps.Copy(out, full)
		return out
	}
	for _, f := range fields {
		out[f] = full[f]
	}
	return out
}

func derived(object string, rec record) record {
	out := maps.Clone(rec)
	switch object {
	case "Opportunity":
		stage, _ := rec["StageName"].(string)
		out["IsClosed"] = strings.HasPrefix(stage, "Closed")
		out["IsWon"] = stage == "Closed Won"
	case "Case":
		out["IsClosed"] = rec["Status"] == "Closed"
	case "Task":
		out["IsClosed"] = rec["Status"] == "Completed"
	}
	return out
}

func attributes(object string, id any) map[string]any {
	return map[string]any{"type": object, "url": fmt.Sprintf("/services/data/v62.0/sobjects/%s/%s", object, id)}
}

// validate applies the org's schema rules: required fields on create, and a
// validation rule on Opportunity amounts.
func validate(object string, values record, creating bool) (code, msg string, fields []string) {
	if creating {
		var missing []string
		for _, f := range required[object] {
			if v, ok := values[f]; !ok || v == nil || v == "" {
				missing = append(missing, f)
			}
		}
		if len(missing) > 0 {
			return "REQUIRED_FIELD_MISSING", fmt.Sprintf("Required fields are missing: [%s]", strings.Join(missing, ", ")), missing
		}
	}
	if object == "Opportunity" {
		if amount, ok := values["Amount"].(float64); ok && amount < 0 {
			return "FIELD_CUSTOM_VALIDATION_EXCEPTION", "Amount can't be negative.", []string{"Amount"}
		}
	}
	for f := range values {
		if f == "Id" || f == "CreatedDate" || f == "LastModifiedDate" || f == "IsClosed" || f == "IsWon" {
			return "INVALID_FIELD_FOR_INSERT_UPDATE", fmt.Sprintf("Unable to create/update fields: %s. Please check the security settings of this field and verify that it is read/write for your profile or permission set.", f), []string{f}
		}
	}
	return "", "", nil
}

func readValues(w http.ResponseWriter, r *http.Request) (record, bool) {
	var values record
	if err := json.NewDecoder(r.Body).Decode(&values); err != nil {
		writeErrors(w, http.StatusBadRequest, "JSON_PARSER_ERROR", "The request body isn't a JSON object: "+err.Error())
		return nil, false
	}
	if values == nil {
		values = record{}
	}
	delete(values, "attributes")
	return values, true
}

func merged(rec, values record) record {
	out := maps.Clone(rec)
	maps.Copy(out, values)
	return out
}

func writeErrors(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, []map[string]any{{"errorCode": code, "message": msg}})
}

func writeFieldErrors(w http.ResponseWriter, code, msg string, fields []string) {
	writeJSON(w, http.StatusBadRequest, []map[string]any{{"errorCode": code, "message": msg, "fields": fields}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json;charset=UTF-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// withChecksum turns a 15-character ID into the 18-character form, as
// Salesforce does: each 5-character chunk adds a character recording which of
// its letters are upper case.
func withChecksum(id string) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ012345"
	suffix := ""
	for chunk := range 3 {
		bits := 0
		for i := range 5 {
			if c := id[chunk*5+i]; c >= 'A' && c <= 'Z' {
				bits |= 1 << i
			}
		}
		suffix += string(alphabet[bits])
	}
	return id + suffix
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x", b)
}

// Records returns copies of an object's records, oldest first, for tests.
func (o *Org) Records(object string) []map[string]any {
	o.mu.Lock()
	defer o.mu.Unlock()
	var out []map[string]any
	for _, rec := range o.records[object] {
		out = append(out, derived(object, rec))
	}
	slices.SortFunc(out, func(a, b map[string]any) int { return strings.Compare(a["Id"].(string), b["Id"].(string)) })
	return out
}
