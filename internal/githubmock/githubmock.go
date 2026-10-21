// Package githubmock is a small, in-memory GitHub for the sandbox and the
// tests. It speaks the REST API the GitHub connector uses, with the same
// paths, payloads, errors and rate limit headers:
//
//   - issues: create, get, update, comment, and search (repo:, is:open,
//     is:closed, is:issue, is:pr, label:, and words in the title or body);
//   - GitHub Actions workflow_dispatch. A dispatched run "runs" for a few
//     seconds; when it finishes it POSTs {workflow, conclusion, runId, url}
//     to its callback_url input, as a workflow's last step would, so a flow
//     can wait for it with a Signal;
//   - a secondary rate limit on creating content: more than a few issues or
//     comments a minute answers 403 with Retry-After.
//
// Repositories acme/storefront and acme/payments are seeded on every start.
package githubmock

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Token is the demo token. The worker uses it by default.
const Token = "ghp_visualWorkflowsDemoToken"

// Latency makes calls take about as long as GitHub's; RunDuration is how long
// a dispatched workflow runs; CreatesPerMinute is the secondary rate limit.
var (
	Latency          = 150 * time.Millisecond
	RunDuration      = 8 * time.Second
	CreatesPerMinute = 20
)

type issue = map[string]any

type repo struct {
	owner, name string
	issues      []issue
	workflows   map[string][]string // file -> declared inputs
	runs        []map[string]any
}

// GitHub is one in-memory GitHub.
type GitHub struct {
	logger *slog.Logger

	mu      sync.Mutex
	repos   map[string]*repo
	creates []time.Time
	nextID  int64
}

// New returns a GitHub with the demo repositories.
func New(logger *slog.Logger) *GitHub {
	g := &GitHub{logger: logger, repos: map[string]*repo{}, nextID: 1_000_000}
	g.seed()
	return g
}

var collaborators = []string{"octocat", "riley", "dana", "sam"}

// Handler serves the API. Mount it under a prefix with http.StripPrefix.
func (g *GitHub) Handler(prefix string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /repos/{owner}/{repo}/issues", g.createIssue)
	mux.HandleFunc("GET /repos/{owner}/{repo}/issues/{number}", g.getIssue)
	mux.HandleFunc("PATCH /repos/{owner}/{repo}/issues/{number}", g.updateIssue)
	mux.HandleFunc("POST /repos/{owner}/{repo}/issues/{number}/comments", g.comment)
	mux.HandleFunc("GET /search/issues", g.search)
	mux.HandleFunc("POST /repos/{owner}/{repo}/actions/workflows/{workflow}/dispatches", g.dispatch)
	mux.HandleFunc("GET /repos/{owner}/{repo}/actions/runs", g.listRuns)
	return http.StripPrefix(prefix, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tok, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); tok != Token {
			if tok, _ = strings.CutPrefix(r.Header.Get("Authorization"), "token "); tok != Token {
				writeError(w, http.StatusUnauthorized, "Bad credentials", nil)
				return
			}
		}
		w.Header().Set("X-RateLimit-Limit", "5000")
		w.Header().Set("X-RateLimit-Remaining", "4999")
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10))
		time.Sleep(Latency)
		mux.ServeHTTP(w, r)
	}))
}

// repoOf finds the request's repository, or answers 404 as GitHub does for a
// repository the token can't see. Callers hold g.mu.
func (g *GitHub) repoOf(w http.ResponseWriter, r *http.Request) *repo {
	rp := g.repos[strings.ToLower(r.PathValue("owner")+"/"+r.PathValue("repo"))]
	if rp == nil {
		writeError(w, http.StatusNotFound, "Not Found", nil)
	}
	return rp
}

// throttle applies the secondary rate limit on creating content. Callers
// hold g.mu.
func (g *GitHub) throttle(w http.ResponseWriter) bool {
	now := time.Now()
	g.creates = slices.DeleteFunc(g.creates, func(t time.Time) bool { return now.Sub(t) > time.Minute })
	if len(g.creates) >= CreatesPerMinute {
		g.logger.Warn("github: secondary rate limit")
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusForbidden, "You have exceeded a secondary rate limit and have been temporarily blocked from content creation. Please retry your request again later.", nil)
		return true
	}
	g.creates = append(g.creates, now)
	return false
}

func (g *GitHub) createIssue(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Title     string   `json:"title"`
		Body      string   `json:"body"`
		Labels    []string `json:"labels"`
		Assignees []string `json:"assignees"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "Problems parsing JSON", nil)
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	rp := g.repoOf(w, r)
	if rp == nil {
		return
	}
	if in.Title == "" {
		writeError(w, http.StatusUnprocessableEntity, "Validation Failed", []map[string]any{{"resource": "Issue", "code": "missing_field", "field": "title"}})
		return
	}
	for _, a := range in.Assignees {
		if !slices.Contains(collaborators, a) {
			writeError(w, http.StatusUnprocessableEntity, "Validation Failed", []map[string]any{{"resource": "Issue", "code": "invalid", "field": "assignees", "value": a}})
			return
		}
	}
	if g.throttle(w) {
		return
	}
	is := g.newIssue(rp, in.Title, in.Body, in.Labels, in.Assignees, false)
	g.logger.Info("github: issue created", "repo", rp.owner+"/"+rp.name, "number", is["number"], "title", in.Title)
	writeJSON(w, http.StatusCreated, is)
}

func (g *GitHub) getIssue(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if is := g.issueOf(w, r); is != nil {
		writeJSON(w, http.StatusOK, is)
	}
}

func (g *GitHub) updateIssue(w http.ResponseWriter, r *http.Request) {
	var in map[string]any
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "Problems parsing JSON", nil)
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	is := g.issueOf(w, r)
	if is == nil {
		return
	}
	if s, ok := in["state"].(string); ok {
		if s != "open" && s != "closed" {
			writeError(w, http.StatusUnprocessableEntity, "Validation Failed", []map[string]any{{"resource": "Issue", "code": "invalid", "field": "state"}})
			return
		}
		is["state"] = s
		is["closed_at"] = nil
		is["state_reason"] = nil
		if s == "closed" {
			is["closed_at"] = stamp()
			is["state_reason"] = cmpOr(in["state_reason"], "completed")
		} else if in["state_reason"] != nil {
			is["state_reason"] = in["state_reason"]
		}
	}
	for _, k := range []string{"title", "body"} {
		if v, ok := in[k].(string); ok {
			is[k] = v
		}
	}
	if ls, ok := in["labels"].([]any); ok {
		names := []string{}
		for _, l := range ls {
			names = append(names, fmt.Sprint(l))
		}
		is["labels"] = labels(names)
	}
	is["updated_at"] = stamp()
	g.logger.Info("github: issue updated", "url", is["html_url"], "state", is["state"])
	writeJSON(w, http.StatusOK, is)
}

func (g *GitHub) comment(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Body string `json:"body"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	g.mu.Lock()
	defer g.mu.Unlock()
	is := g.issueOf(w, r)
	if is == nil {
		return
	}
	if in.Body == "" {
		writeError(w, http.StatusUnprocessableEntity, "Validation Failed", []map[string]any{{"resource": "IssueComment", "code": "missing_field", "field": "body"}})
		return
	}
	if g.throttle(w) {
		return
	}
	g.nextID++
	is["comments"] = is["comments"].(int) + 1
	c := map[string]any{
		"id": g.nextID, "body": in.Body, "user": map[string]any{"login": "visual-workflows[bot]"}, "created_at": stamp(),
		"html_url": fmt.Sprintf("%s#issuecomment-%d", is["html_url"], g.nextID),
	}
	g.logger.Info("github: comment", "url", is["html_url"], "body", in.Body)
	writeJSON(w, http.StatusCreated, c)
}

func (g *GitHub) search(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if strings.TrimSpace(q) == "" {
		writeError(w, http.StatusUnprocessableEntity, "Validation Failed", []map[string]any{{"resource": "Search", "field": "q", "code": "missing"}})
		return
	}
	var repoFilter, state, kind string
	var labelFilters, words []string
	for _, tok := range strings.Fields(q) {
		k, v, qualified := strings.Cut(tok, ":")
		switch {
		case qualified && k == "repo":
			repoFilter = strings.ToLower(v)
		case qualified && k == "is" && (v == "open" || v == "closed"):
			state = v
		case qualified && k == "is" && (v == "issue" || v == "pr"):
			kind = v
		case qualified && k == "label":
			labelFilters = append(labelFilters, strings.Trim(v, `"`))
		case qualified && k == "state":
			state = v
		default:
			words = append(words, strings.ToLower(tok))
		}
	}
	g.mu.Lock()
	items := []issue{}
	for key, rp := range g.repos {
		if repoFilter != "" && key != repoFilter {
			continue
		}
		for _, is := range rp.issues {
			if state != "" && is["state"] != state {
				continue
			}
			_, isPR := is["pull_request"]
			if (kind == "issue" && isPR) || (kind == "pr" && !isPR) {
				continue
			}
			names := labelNames(is)
			if slices.ContainsFunc(labelFilters, func(l string) bool { return !slices.Contains(names, l) }) {
				continue
			}
			text := strings.ToLower(fmt.Sprint(is["title"], " ", is["body"]))
			if slices.ContainsFunc(words, func(word string) bool { return !strings.Contains(text, word) }) {
				continue
			}
			items = append(items, is)
		}
	}
	g.mu.Unlock()
	slices.SortFunc(items, func(a, b issue) int { return strings.Compare(b["created_at"].(string), a["created_at"].(string)) })
	writeJSON(w, http.StatusOK, map[string]any{"total_count": len(items), "incomplete_results": false, "items": items})
}

func (g *GitHub) dispatch(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Ref    string         `json:"ref"`
		Inputs map[string]any `json:"inputs"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "Problems parsing JSON", nil)
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	rp := g.repoOf(w, r)
	if rp == nil {
		return
	}
	file := r.PathValue("workflow")
	declared, ok := rp.workflows[file]
	switch {
	case !ok:
		writeError(w, http.StatusNotFound, "Not Found", nil)
		return
	case in.Ref != "main":
		writeError(w, http.StatusUnprocessableEntity, "No ref found for: "+in.Ref, nil)
		return
	}
	var unexpected []string
	for k := range in.Inputs {
		if !slices.Contains(declared, k) {
			unexpected = append(unexpected, strconv.Quote(k))
		}
	}
	if len(unexpected) > 0 {
		slices.Sort(unexpected)
		writeError(w, http.StatusUnprocessableEntity, "Unexpected inputs provided: ["+strings.Join(unexpected, ", ")+"]", nil)
		return
	}
	g.nextID++
	id := g.nextID
	url := fmt.Sprintf("https://github.com/%s/%s/actions/runs/%d", rp.owner, rp.name, id)
	run := map[string]any{"id": id, "name": file, "head_branch": in.Ref, "event": "workflow_dispatch", "status": "in_progress", "conclusion": nil, "html_url": url, "inputs": in.Inputs, "created_at": stamp()}
	rp.runs = append(rp.runs, run)
	g.logger.Info("github: workflow dispatched", "repo", rp.owner+"/"+rp.name, "workflow", file, "inputs", in.Inputs)
	callbackURL, _ := in.Inputs["callback_url"].(string)
	time.AfterFunc(RunDuration, func() {
		g.mu.Lock()
		run["status"], run["conclusion"] = "completed", "success"
		g.mu.Unlock()
		g.logger.Info("github: workflow run finished", "workflow", file, "run", id, "conclusion", "success")
		if callbackURL != "" {
			g.callback(callbackURL, map[string]any{"workflow": file, "conclusion": "success", "runId": id, "url": url, "inputs": in.Inputs})
		}
	})
	w.WriteHeader(http.StatusNoContent)
}

func (g *GitHub) listRuns(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	rp := g.repoOf(w, r)
	if rp == nil {
		return
	}
	runs := slices.Clone(rp.runs)
	slices.Reverse(runs)
	writeJSON(w, http.StatusOK, map[string]any{"total_count": len(runs), "workflow_runs": runs})
}

// callback POSTs a finished run's result, retrying briefly as a workflow's
// curl step would.
func (g *GitHub) callback(url string, body map[string]any) {
	raw, _ := json.Marshal(body)
	for attempt := 1; attempt <= 5; attempt++ {
		resp, err := http.Post(url, "application/json", bytes.NewReader(raw))
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 300 {
				g.logger.Info("github: workflow called back", "url", url)
				return
			}
			err = fmt.Errorf("status %s", resp.Status)
		}
		g.logger.Warn("github: callback failed; retrying", "url", url, "attempt", attempt, "error", err)
		time.Sleep(time.Duration(attempt) * time.Second)
	}
}

// issueOf finds the request's issue. Callers hold g.mu.
func (g *GitHub) issueOf(w http.ResponseWriter, r *http.Request) issue {
	rp := g.repoOf(w, r)
	if rp == nil {
		return nil
	}
	n, _ := strconv.Atoi(r.PathValue("number"))
	i := slices.IndexFunc(rp.issues, func(is issue) bool { return is["number"] == n })
	if i < 0 {
		writeError(w, http.StatusNotFound, "Not Found", nil)
		return nil
	}
	return rp.issues[i]
}

// newIssue adds an issue (or a pull request) to a repository. Issues and
// pull requests share numbers, as on GitHub. Callers hold g.mu.
func (g *GitHub) newIssue(rp *repo, title, body string, labelNames, assignees []string, pr bool) issue {
	g.nextID++
	n := len(rp.issues) + 1
	kind := "issues"
	if pr {
		kind = "pull"
	}
	as := []any{}
	for _, a := range assignees {
		as = append(as, map[string]any{"login": a})
	}
	is := issue{
		"id": g.nextID, "number": n, "title": title, "body": body, "state": "open", "state_reason": nil,
		"labels": labels(labelNames), "assignees": as, "comments": 0, "user": map[string]any{"login": "visual-workflows[bot]"},
		"html_url":   fmt.Sprintf("https://github.com/%s/%s/%s/%d", rp.owner, rp.name, kind, n),
		"url":        fmt.Sprintf("https://api.github.com/repos/%s/%s/issues/%d", rp.owner, rp.name, n),
		"created_at": stamp(), "updated_at": stamp(), "closed_at": nil,
	}
	if pr {
		is["pull_request"] = map[string]any{"html_url": is["html_url"]}
	}
	rp.issues = append(rp.issues, is)
	return is
}

// Issues returns copies of a repository's issues, for tests.
func (g *GitHub) Issues(fullName string) []map[string]any {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []map[string]any
	for _, is := range g.repos[fullName].issues {
		c := map[string]any{}
		for k, v := range is {
			c[k] = v
		}
		out = append(out, c)
	}
	return out
}

func (g *GitHub) seed() {
	add := func(owner, name string, workflows map[string][]string) *repo {
		rp := &repo{owner: owner, name: name, workflows: workflows}
		g.repos[owner+"/"+name] = rp
		return rp
	}
	store := add("acme", "storefront", map[string][]string{
		"deploy.yml":   {"service", "version", "callback_url"},
		"rollback.yml": {"service", "reason", "callback_url"},
	})
	g.newIssue(store, "Search results slow for some users", "p95 latency on /search is above 2s since Tuesday.", []string{"bug", "performance"}, nil, false)
	g.newIssue(store, "Checkout shows the wrong currency for EU visitors", "Prices show $ instead of €.", []string{"bug"}, []string{"dana"}, false)
	g.newIssue(store, "Add Apple Pay to checkout", "", []string{"enhancement"}, nil, false)
	g.newIssue(store, "Cache product images at the edge", "Moves images to the CDN.", nil, []string{"riley"}, true)
	pay := add("acme", "payments", map[string][]string{"deploy.yml": {"service", "version", "callback_url"}})
	g.newIssue(pay, "Retry webhooks from the card processor", "", []string{"bug"}, nil, false)
}

func labels(names []string) []any {
	out := []any{}
	for _, n := range names {
		out = append(out, map[string]any{"name": n, "color": "ededed"})
	}
	return out
}

func labelNames(is issue) []string {
	var names []string
	for _, l := range is["labels"].([]any) {
		names = append(names, l.(map[string]any)["name"].(string))
	}
	return names
}

func writeError(w http.ResponseWriter, status int, msg string, errs []map[string]any) {
	body := map[string]any{"message": msg, "documentation_url": "https://docs.github.com/rest", "status": strconv.Itoa(status)}
	if errs != nil {
		body["errors"] = errs
	}
	writeJSON(w, status, body)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func stamp() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func cmpOr(v any, fallback string) any {
	if s, ok := v.(string); ok && s != "" {
		return s
	}
	return fallback
}
