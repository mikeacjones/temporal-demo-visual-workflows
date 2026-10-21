// Package servicenowmock is a small, in-memory ServiceNow instance for the
// sandbox and the tests. It speaks the Table API the ServiceNow connector
// uses, with the same paths, payloads and errors:
//
//   - basic auth;
//   - GET, POST, PATCH and DELETE on /api/now/table/{table}[/{sys_id}], with
//     sysparm_query (an encoded query: ^ and ^OR, =, !=, <, >, LIKE,
//     STARTSWITH, IN, ISEMPTY, ISNOTEMPTY, ORDERBY and ORDERBYDESC),
//     sysparm_fields and sysparm_limit;
//   - field values as strings, numbers such as INC0010001, an incident's
//     priority worked out from its urgency and impact;
//   - a data policy: resolving or closing an incident needs a resolution code
//     and notes, or the update fails with 403.
//
// The data lives in memory and is seeded fresh on every start.
package servicenowmock

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

// The demo instance's login. The worker uses it by default.
const (
	User     = "admin"
	Password = "visual-workflows-demo"
)

// Latency makes calls take about as long as a real instance's.
var Latency = 150 * time.Millisecond

type record = map[string]string

// Number prefixes of the task tables.
var numbers = map[string]string{"incident": "INC", "change_request": "CHG", "problem": "PRB", "sc_request": "REQ", "sc_req_item": "RITM", "task": "TASK"}

// Instance is one in-memory instance.
type Instance struct {
	logger *slog.Logger

	mu      sync.Mutex
	tables  map[string]map[string]record // table -> sys_id -> fields
	counter int
}

// New returns an instance seeded with demo data.
func New(logger *slog.Logger) *Instance {
	in := &Instance{logger: logger, tables: map[string]map[string]record{}, counter: 10000}
	in.seed()
	return in
}

// Handler serves the Table API. Mount it under a prefix with
// http.StripPrefix.
func (in *Instance) Handler(prefix string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/now/table/{table}", in.query)
	mux.HandleFunc("POST /api/now/table/{table}", in.create)
	mux.HandleFunc("GET /api/now/table/{table}/{id}", in.get)
	mux.HandleFunc("PATCH /api/now/table/{table}/{id}", in.update)
	mux.HandleFunc("PUT /api/now/table/{table}/{id}", in.update)
	mux.HandleFunc("DELETE /api/now/table/{table}/{id}", in.delete)
	return http.StripPrefix(prefix, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); !ok || u != User || p != Password {
			writeError(w, http.StatusUnauthorized, "User Not Authenticated", "Required to provide Auth information")
			return
		}
		time.Sleep(Latency)
		mux.ServeHTTP(w, r)
	}))
}

func (in *Instance) table(w http.ResponseWriter, r *http.Request) (map[string]record, bool) {
	t, ok := in.tables[r.PathValue("table")]
	if !ok {
		writeError(w, http.StatusBadRequest, "Invalid table "+r.PathValue("table"), "")
	}
	return t, ok
}

func (in *Instance) query(w http.ResponseWriter, r *http.Request) {
	in.mu.Lock()
	defer in.mu.Unlock()
	t, ok := in.table(w, r)
	if !ok {
		return
	}
	q := parseQuery(r.URL.Query().Get("sysparm_query"))
	var rows []record
	for _, rec := range t {
		if q.matches(rec) {
			rows = append(rows, rec)
		}
	}
	slices.SortFunc(rows, func(a, b record) int {
		return strings.Compare(a["sys_created_on"]+a["sys_id"], b["sys_created_on"]+b["sys_id"])
	})
	for i := len(q.order) - 1; i >= 0; i-- {
		o := q.order[i]
		slices.SortStableFunc(rows, func(a, b record) int {
			n := compare(a[o.field], b[o.field])
			if o.desc {
				n = -n
			}
			return n
		})
	}
	if limit, err := strconv.Atoi(r.URL.Query().Get("sysparm_limit")); err == nil && limit >= 0 && limit < len(rows) {
		rows = rows[:limit]
	}
	fields := fieldList(r)
	out := make([]record, 0, len(rows))
	for _, rec := range rows {
		out = append(out, pick(rec, fields))
	}
	in.logger.Info("servicenow: query", "table", r.PathValue("table"), "query", r.URL.Query().Get("sysparm_query"), "rows", len(out))
	w.Header().Set("X-Total-Count", strconv.Itoa(len(out)))
	writeJSON(w, http.StatusOK, map[string]any{"result": out})
}

func (in *Instance) get(w http.ResponseWriter, r *http.Request) {
	in.mu.Lock()
	defer in.mu.Unlock()
	t, ok := in.table(w, r)
	if !ok {
		return
	}
	rec, ok := t[r.PathValue("id")]
	if !ok {
		writeError(w, http.StatusNotFound, "No Record found", "Record doesn't exist or ACL restricts the record retrieval")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"result": pick(rec, fieldList(r))})
}

func (in *Instance) create(w http.ResponseWriter, r *http.Request) {
	values, ok := readValues(w, r)
	if !ok {
		return
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	table := r.PathValue("table")
	t, ok := in.table(w, r)
	if !ok {
		return
	}
	rec := in.insert(table, values)
	if msg := policy(table, rec); msg != "" {
		delete(t, rec["sys_id"])
		writeError(w, http.StatusForbidden, "Operation Failed", msg)
		return
	}
	in.logger.Info("servicenow: record created", "table", table, "number", rec["number"], "sys_id", rec["sys_id"])
	writeJSON(w, http.StatusCreated, map[string]any{"result": rec})
}

func (in *Instance) update(w http.ResponseWriter, r *http.Request) {
	values, ok := readValues(w, r)
	if !ok {
		return
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	table := r.PathValue("table")
	t, ok := in.table(w, r)
	if !ok {
		return
	}
	rec, ok := t[r.PathValue("id")]
	if !ok {
		writeError(w, http.StatusNotFound, "No Record found", "Record doesn't exist or ACL restricts the record retrieval")
		return
	}
	next := record{}
	for k, v := range rec {
		next[k] = v
	}
	for k, v := range values {
		next[k] = v
	}
	derive(table, next, time.Now())
	if msg := policy(table, next); msg != "" {
		in.logger.Warn("servicenow: data policy refused an update", "table", table, "number", rec["number"])
		writeError(w, http.StatusForbidden, "Operation Failed", msg)
		return
	}
	next["sys_updated_on"] = now()
	next["sys_mod_count"] = strconv.Itoa(atoi(rec["sys_mod_count"]) + 1)
	t[rec["sys_id"]] = next
	in.logger.Info("servicenow: record updated", "table", table, "number", next["number"], "fields", slices.Sorted(maps.Keys(values)))
	writeJSON(w, http.StatusOK, map[string]any{"result": next})
}

func (in *Instance) delete(w http.ResponseWriter, r *http.Request) {
	in.mu.Lock()
	defer in.mu.Unlock()
	t, ok := in.table(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if _, ok := t[id]; !ok {
		writeError(w, http.StatusNotFound, "No Record found", "Record doesn't exist or ACL restricts the record retrieval")
		return
	}
	in.logger.Info("servicenow: record deleted", "table", r.PathValue("table"), "number", t[id]["number"])
	delete(t, id)
	w.WriteHeader(http.StatusNoContent)
}

// insert stores a record with the system fields a real instance fills in.
// Callers hold in.mu.
func (in *Instance) insert(table string, values record) record {
	ts := now()
	rec := record{
		"sys_id": newSysID(), "sys_created_on": ts, "sys_updated_on": ts, "sys_created_by": User,
		"sys_updated_by": User, "sys_mod_count": "0", "active": "true",
	}
	if prefix, ok := numbers[table]; ok {
		in.counter++
		rec["number"] = fmt.Sprintf("%s%07d", prefix, in.counter)
		rec["state"] = "1"
		if table == "change_request" {
			rec["state"] = "-5"
		}
	}
	for k, v := range values {
		rec[k] = v
	}
	derive(table, rec, time.Now())
	in.tables[table][rec["sys_id"]] = rec
	return rec
}

// derive keeps computed fields in step: an incident's priority from its
// urgency and impact, and when it was resolved or closed.
func derive(table string, rec record, at time.Time) {
	if table != "incident" {
		return
	}
	u, i := cmp.Or(rec["urgency"], "3"), cmp.Or(rec["impact"], "3")
	rec["urgency"], rec["impact"] = u, i
	rec["priority"] = strconv.Itoa(min(5, max(1, atoi(u)+atoi(i)-1)))
	switch rec["state"] {
	case "6":
		if rec["resolved_at"] == "" {
			rec["resolved_at"] = at.UTC().Format("2006-01-02 15:04:05")
		}
	case "7", "8":
		rec["active"] = "false"
		if rec["closed_at"] == "" {
			rec["closed_at"] = at.UTC().Format("2006-01-02 15:04:05")
		}
	}
}

// policy is the instance's data policy: a resolved or closed incident needs
// a resolution code and notes.
func policy(table string, rec record) string {
	if table == "incident" && (rec["state"] == "6" || rec["state"] == "7") {
		var missing []string
		if rec["close_code"] == "" {
			missing = append(missing, "Resolution code")
		}
		if rec["close_notes"] == "" {
			missing = append(missing, "Resolution notes")
		}
		if len(missing) > 0 {
			return "Data Policy Exception: \n The following fields are mandatory: " + strings.Join(missing, ", ")
		}
	}
	return ""
}

// Records returns copies of a table's records, oldest first, for tests.
func (in *Instance) Records(table string) []map[string]string {
	in.mu.Lock()
	defer in.mu.Unlock()
	var out []map[string]string
	for _, rec := range in.tables[table] {
		out = append(out, pick(rec, nil))
	}
	slices.SortFunc(out, func(a, b map[string]string) int {
		return strings.Compare(a["number"]+a["sys_id"], b["number"]+b["sys_id"])
	})
	return out
}

func (in *Instance) seed() {
	for _, t := range []string{"incident", "change_request", "problem", "sc_request", "sys_user", "cmdb_ci_service"} {
		in.tables[t] = map[string]record{}
	}
	in.insert("sys_user", record{"user_name": "riley.chen", "name": "Riley Chen", "email": "riley@example.com", "title": "Site Reliability Engineer"})
	in.insert("sys_user", record{"user_name": "dana.ortiz", "name": "Dana Ortiz", "email": "dana@example.com", "title": "Account Executive"})
	for _, s := range []string{"Storefront", "Payments API", "Search", "Checkout"} {
		in.insert("cmdb_ci_service", record{"name": s, "operational_status": "1"})
	}
	in.insert("incident", record{"short_description": "Search results slow for some users", "urgency": "2", "impact": "3", "category": "software", "cmdb_ci": "Search", "state": "2"})
	in.insert("incident", record{"short_description": "Checkout page shows wrong currency", "urgency": "2", "impact": "2", "category": "software", "cmdb_ci": "Checkout", "state": "1"})
	in.insert("incident", record{"short_description": "Password reset emails delayed", "urgency": "3", "impact": "3", "category": "inquiry", "state": "6", "close_code": "Solved (Permanently)", "close_notes": "Mail queue drained."})
	in.insert("change_request", record{"short_description": "Upgrade Payments API to v3", "type": "normal", "cmdb_ci": "Payments API"})
}

type condition struct {
	field, op, value string
	or               []condition
}

type ordering struct {
	field string
	desc  bool
}

type encodedQuery struct {
	conds []condition
	order []ordering
}

var operators = []string{"ISNOTEMPTY", "ISEMPTY", "STARTSWITH", "ENDSWITH", "NOT LIKE", "NOT IN", "LIKE", "IN", "!=", ">=", "<=", "=", ">", "<"}

// parseQuery reads an encoded query: conditions joined by ^ (and), with ^OR
// adding an alternative to the condition before it.
func parseQuery(s string) encodedQuery {
	var q encodedQuery
	for _, term := range strings.Split(s, "^") {
		term = strings.TrimSpace(term)
		switch {
		case term == "":
		case strings.HasPrefix(term, "ORDERBYDESC"):
			q.order = append(q.order, ordering{strings.TrimPrefix(term, "ORDERBYDESC"), true})
		case strings.HasPrefix(term, "ORDERBY"):
			q.order = append(q.order, ordering{strings.TrimPrefix(term, "ORDERBY"), false})
		case strings.HasPrefix(term, "OR") && len(q.conds) > 0:
			last := &q.conds[len(q.conds)-1]
			last.or = append(last.or, parseCondition(strings.TrimPrefix(term, "OR")))
		default:
			q.conds = append(q.conds, parseCondition(term))
		}
	}
	return q
}

func parseCondition(term string) condition {
	end := strings.IndexFunc(term, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '.')
	})
	if end < 0 {
		return condition{field: term, op: "ISNOTEMPTY"}
	}
	field, rest := term[:end], term[end:]
	for _, op := range operators {
		if v, ok := strings.CutPrefix(rest, op); ok {
			return condition{field: field, op: op, value: v}
		}
	}
	return condition{field: field, op: "=", value: rest}
}

func (q encodedQuery) matches(rec record) bool {
	for _, c := range q.conds {
		ok := c.matches(rec)
		for _, alt := range c.or {
			ok = ok || alt.matches(rec)
		}
		if !ok {
			return false
		}
	}
	return true
}

func (c condition) matches(rec record) bool {
	v := rec[c.field]
	switch c.op {
	case "=":
		return strings.EqualFold(v, c.value)
	case "!=":
		return !strings.EqualFold(v, c.value)
	case "LIKE":
		return strings.Contains(strings.ToLower(v), strings.ToLower(c.value))
	case "NOT LIKE":
		return !strings.Contains(strings.ToLower(v), strings.ToLower(c.value))
	case "STARTSWITH":
		return strings.HasPrefix(strings.ToLower(v), strings.ToLower(c.value))
	case "ENDSWITH":
		return strings.HasSuffix(strings.ToLower(v), strings.ToLower(c.value))
	case "IN", "NOT IN":
		in := slices.ContainsFunc(strings.Split(c.value, ","), func(x string) bool { return strings.EqualFold(strings.TrimSpace(x), v) })
		return in == (c.op == "IN")
	case "ISEMPTY":
		return v == ""
	case "ISNOTEMPTY":
		return v != ""
	case ">":
		return compare(v, c.value) > 0
	case ">=":
		return compare(v, c.value) >= 0
	case "<":
		return compare(v, c.value) < 0
	case "<=":
		return compare(v, c.value) <= 0
	}
	return false
}

// compare orders numbers as numbers and anything else as text.
func compare(a, b string) int {
	x, errA := strconv.ParseFloat(a, 64)
	y, errB := strconv.ParseFloat(b, 64)
	if errA == nil && errB == nil {
		switch {
		case x < y:
			return -1
		case x > y:
			return 1
		}
		return 0
	}
	return strings.Compare(strings.ToLower(a), strings.ToLower(b))
}

func readValues(w http.ResponseWriter, r *http.Request) (record, bool) {
	var raw map[string]any
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeError(w, http.StatusBadRequest, "Exception while reading request", "The payload is not valid JSON.")
		return nil, false
	}
	values := record{}
	for k, v := range raw {
		switch x := v.(type) {
		case nil:
			values[k] = ""
		case string:
			values[k] = x
		case float64:
			values[k] = strconv.FormatFloat(x, 'f', -1, 64)
		default:
			values[k] = fmt.Sprint(x)
		}
	}
	return values, true
}

func fieldList(r *http.Request) []string {
	var fields []string
	for _, f := range strings.Split(r.URL.Query().Get("sysparm_fields"), ",") {
		if f = strings.TrimSpace(f); f != "" {
			fields = append(fields, f)
		}
	}
	return fields
}

func pick(rec record, fields []string) record {
	out := record{}
	if len(fields) == 0 {
		for k, v := range rec {
			out[k] = v
		}
		return out
	}
	for _, f := range fields {
		out[f] = rec[f]
	}
	return out
}

func writeError(w http.ResponseWriter, status int, message, detail string) {
	var d any
	if detail != "" {
		d = detail
	}
	writeJSON(w, status, map[string]any{"error": map[string]any{"message": message, "detail": d}, "status": "failure"})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json;charset=UTF-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func newSysID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x", b)
}

func now() string { return time.Now().UTC().Format("2006-01-02 15:04:05") }

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
