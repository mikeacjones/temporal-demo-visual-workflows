package workflows

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"go.temporal.io/sdk/activity"
)

// GitHubConnection is the worker's GitHub token.
type GitHubConnection struct {
	URL   string // https://api.github.com, or the sandbox's
	Token string
	HTTP  *http.Client
}

// GitHubRequest is one REST API call.
type GitHubRequest struct {
	Repo        string         `json:"repo,omitempty"`
	Number      float64        `json:"number,omitempty"`
	Title       string         `json:"title,omitempty"`
	Body        string         `json:"body,omitempty"`
	Labels      []string       `json:"labels,omitempty"`
	Assignees   []string       `json:"assignees,omitempty"`
	State       string         `json:"state,omitempty"`
	StateReason string         `json:"stateReason,omitempty"`
	Query       string         `json:"query,omitempty"`
	Workflow    string         `json:"workflow,omitempty"`
	Ref         string         `json:"ref,omitempty"`
	Inputs      map[string]any `json:"inputs,omitempty"`
}

// GitHubCreateIssue opens an issue: {number, url, state, ...}.
func (a *Activities) GitHubCreateIssue(ctx context.Context, req GitHubRequest) (map[string]any, error) {
	body := map[string]any{"title": req.Title, "body": req.Body}
	if len(req.Labels) > 0 {
		body["labels"] = req.Labels
	}
	if len(req.Assignees) > 0 {
		body["assignees"] = req.Assignees
	}
	var issue map[string]any
	if err := a.callGitHub(ctx, http.MethodPost, repoPath(req.Repo)+"/issues", body, &issue); err != nil {
		return nil, err
	}
	activity.GetLogger(ctx).Info("github issue created", "repo", req.Repo, "number", issue["number"])
	return trimIssue(issue), nil
}

// GitHubComment comments on an issue or pull request: {id, url}.
func (a *Activities) GitHubComment(ctx context.Context, req GitHubRequest) (map[string]any, error) {
	var c struct {
		ID      int64  `json:"id"`
		HTMLURL string `json:"html_url"`
	}
	if err := a.callGitHub(ctx, http.MethodPost, issuePath(req)+"/comments", map[string]any{"body": req.Body}, &c); err != nil {
		return nil, err
	}
	return map[string]any{"id": c.ID, "url": c.HTMLURL}, nil
}

// GitHubUpdateIssue changes the fields given, and only those.
func (a *Activities) GitHubUpdateIssue(ctx context.Context, req GitHubRequest) (map[string]any, error) {
	body := map[string]any{}
	for k, v := range map[string]string{"state": req.State, "state_reason": req.StateReason, "title": req.Title, "body": req.Body} {
		if v != "" {
			body[k] = v
		}
	}
	if len(req.Labels) > 0 {
		body["labels"] = req.Labels
	}
	var issue map[string]any
	if err := a.callGitHub(ctx, http.MethodPatch, issuePath(req), body, &issue); err != nil {
		return nil, err
	}
	return trimIssue(issue), nil
}

// GitHubGetIssue reads an issue or pull request.
func (a *Activities) GitHubGetIssue(ctx context.Context, req GitHubRequest) (map[string]any, error) {
	var issue map[string]any
	if err := a.callGitHub(ctx, http.MethodGet, issuePath(req), nil, &issue); err != nil {
		return nil, err
	}
	out := trimIssue(issue)
	out["body"], out["comments"] = issue["body"], issue["comments"]
	return out, nil
}

// GitHubSearchIssues searches issues and pull requests: {totalCount, items}.
func (a *Activities) GitHubSearchIssues(ctx context.Context, req GitHubRequest) (map[string]any, error) {
	var res struct {
		TotalCount int              `json:"total_count"`
		Items      []map[string]any `json:"items"`
	}
	if err := a.callGitHub(ctx, http.MethodGet, "/search/issues?"+url.Values{"q": {req.Query}, "per_page": {"50"}}.Encode(), nil, &res); err != nil {
		return nil, err
	}
	items := make([]any, 0, len(res.Items))
	for _, it := range res.Items {
		items = append(items, trimIssue(it))
	}
	return map[string]any{"totalCount": res.TotalCount, "items": items}, nil
}

// GitHubDispatchWorkflow starts a GitHub Actions workflow with a
// workflow_dispatch trigger. GitHub answers as soon as the run is queued.
func (a *Activities) GitHubDispatchWorkflow(ctx context.Context, req GitHubRequest) (map[string]any, error) {
	inputs := map[string]string{}
	for k, v := range req.Inputs {
		inputs[k] = fmt.Sprint(v) // workflow_dispatch inputs are strings
	}
	path := repoPath(req.Repo) + "/actions/workflows/" + url.PathEscape(req.Workflow) + "/dispatches"
	if err := a.callGitHub(ctx, http.MethodPost, path, map[string]any{"ref": req.Ref, "inputs": inputs}, nil); err != nil {
		return nil, err
	}
	activity.GetLogger(ctx).Info("github workflow dispatched", "repo", req.Repo, "workflow", req.Workflow, "ref", req.Ref)
	return map[string]any{"dispatched": true, "workflow": req.Workflow, "ref": req.Ref}, nil
}

func repoPath(repo string) string {
	owner, name, _ := strings.Cut(repo, "/")
	return "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name)
}

func issuePath(req GitHubRequest) string {
	return repoPath(req.Repo) + "/issues/" + strconv.Itoa(int(req.Number))
}

// trimIssue keeps the fields later steps use, so outputs stay small.
func trimIssue(issue map[string]any) map[string]any {
	labels := []any{}
	if ls, ok := issue["labels"].([]any); ok {
		for _, l := range ls {
			if m, ok := l.(map[string]any); ok {
				labels = append(labels, m["name"])
			}
		}
	}
	_, isPR := issue["pull_request"]
	return map[string]any{
		"number": issue["number"], "url": issue["html_url"], "state": issue["state"], "stateReason": issue["state_reason"],
		"title": issue["title"], "labels": labels, "pullRequest": isPR,
	}
}

func (a *Activities) callGitHub(ctx context.Context, method, path string, body, out any) error {
	gh := a.GitHub
	if gh == nil || gh.Token == "" {
		return notConfigured("GITHUB_TOKEN")
	}
	defer keepAlive(ctx, "waiting for GitHub: "+method+" "+truncate(path, 60))()
	var payload []byte
	if body != nil {
		var err error
		if payload, err = jsonBody(body); err != nil {
			return err
		}
	}
	header := bearer(gh.Token)
	header.Set("Accept", "application/vnd.github+json")
	header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := apiCall(ctx, gh.HTTP, method, gh.URL+path, header, "application/json", payload)
	if err != nil {
		return err
	}
	if resp.Status >= 200 && resp.Status < 300 {
		return resp.decodeInto("GitHub", out)
	}
	return gitHubError(method, path, resp, time.Now())
}

// gitHubError: a rate limit (403 or 429 with no requests remaining, or a
// Retry-After) is retried when GitHub says it resets; server errors are
// retried; anything else (bad credentials, not found, validation) is not.
func gitHubError(method, path string, resp apiResponse, now time.Time) error {
	var body struct {
		Message string `json:"message"`
		Errors  []struct {
			Field   string `json:"field"`
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	_ = resp.decodeInto("GitHub", &body)
	msg := fmt.Sprintf("GitHub %d on %s %s: %s", resp.Status, method, truncate(path, 80), cmp.Or(body.Message, http.StatusText(resp.Status)))
	for _, e := range body.Errors {
		msg += fmt.Sprintf(" (%s)", strings.TrimSpace(cmp.Or(e.Message, e.Field+" "+e.Code)))
	}

	rateLimited := resp.Header.Get("Retry-After") != "" ||
		((resp.Status == 403 || resp.Status == 429) && resp.Header.Get("X-RateLimit-Remaining") == "0")
	if rateLimited {
		delay, _ := retryAfter(resp.Header.Get("Retry-After"), now)
		if reset, err := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64); err == nil && delay == 0 {
			delay = min(max(time.Unix(reset, 0).Sub(now), time.Second), maxRetryAfter)
		}
		return connectorError(msg, "GitHubRateLimited", true, delay, body)
	}
	types := map[int]string{401: "GitHubBadCredentials", 403: "GitHubForbidden", 404: "GitHubNotFound", 422: "GitHubValidationFailed"}
	if resp.Status == 404 {
		msg += "; check the repository name, and that the token can see it"
	}
	if resp.Status >= 500 {
		return connectorError(msg, "GitHubUnavailable", true, 0, body)
	}
	return connectorError(msg, cmp.Or(types[resp.Status], "GitHubError"), false, 0, body)
}
