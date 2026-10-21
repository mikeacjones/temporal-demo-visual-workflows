package workflows

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"time"

	"go.temporal.io/sdk/activity"
)

// SlackConnection is the worker's Slack bot.
type SlackConnection struct {
	URL   string // https://slack.com/api, or the sandbox's
	Token string
	HTTP  *http.Client
}

// SlackRequest is one Web API call.
type SlackRequest struct {
	Channel  string `json:"channel,omitempty"`
	Text     string `json:"text,omitempty"`
	Blocks   any    `json:"blocks,omitempty"`
	ThreadTS string `json:"threadTs,omitempty"`
	TS       string `json:"ts,omitempty"`
	Name     string `json:"name,omitempty"`
	Email    string `json:"email,omitempty"`
}

// SlackPostMessage posts a message: {channel, ts}. The channel in the result
// is its ID, which updating or reacting to the message needs.
func (a *Activities) SlackPostMessage(ctx context.Context, req SlackRequest) (map[string]any, error) {
	body := map[string]any{"channel": req.Channel, "text": req.Text}
	if req.Blocks != nil {
		body["blocks"] = req.Blocks
	}
	if req.ThreadTS != "" {
		body["thread_ts"] = req.ThreadTS
	}
	var res struct {
		Channel string `json:"channel"`
		TS      string `json:"ts"`
	}
	if err := a.callSlack(ctx, "chat.postMessage", body, &res); err != nil {
		return nil, err
	}
	activity.GetLogger(ctx).Info("slack message posted", "channel", res.Channel, "ts", res.TS)
	return map[string]any{"channel": res.Channel, "ts": res.TS}, nil
}

// SlackUpdateMessage replaces a message's text (and blocks).
func (a *Activities) SlackUpdateMessage(ctx context.Context, req SlackRequest) (map[string]any, error) {
	body := map[string]any{"channel": req.Channel, "ts": req.TS, "text": req.Text}
	if req.Blocks != nil {
		body["blocks"] = req.Blocks
	}
	var res struct {
		Channel string `json:"channel"`
		TS      string `json:"ts"`
	}
	if err := a.callSlack(ctx, "chat.update", body, &res); err != nil {
		return nil, err
	}
	return map[string]any{"channel": res.Channel, "ts": res.TS}, nil
}

// SlackAddReaction adds an emoji reaction. One that is already there is
// fine, so a retry succeeds.
func (a *Activities) SlackAddReaction(ctx context.Context, req SlackRequest) (map[string]any, error) {
	err := a.callSlack(ctx, "reactions.add", map[string]any{"channel": req.Channel, "timestamp": req.TS, "name": req.Name}, nil)
	if err != nil && errorType(err) != "already_reacted" {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

// SlackDeleteMessage deletes a message. One that is already gone counts as
// deleted, so a retried delete (or an undo) succeeds.
func (a *Activities) SlackDeleteMessage(ctx context.Context, req SlackRequest) (map[string]any, error) {
	err := a.callSlack(ctx, "chat.delete", map[string]any{"channel": req.Channel, "ts": req.TS}, nil)
	if err != nil && errorType(err) != "message_not_found" {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

// SlackFindUser looks a user up by email: {found, user}.
func (a *Activities) SlackFindUser(ctx context.Context, req SlackRequest) (map[string]any, error) {
	var res struct {
		User struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			RealName string `json:"real_name"`
		} `json:"user"`
	}
	err := a.callSlack(ctx, "users.lookupByEmail?"+url.Values{"email": {req.Email}}.Encode(), nil, &res)
	if errorType(err) == "users_not_found" {
		return map[string]any{"found": false, "user": nil}, nil
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"found": true, "user": map[string]any{"id": res.User.ID, "name": res.User.Name, "realName": res.User.RealName}}, nil
}

// Slack errors worth another try; any other is the request's fault.
var retryableSlackErrors = []string{"ratelimited", "internal_error", "fatal_error", "service_unavailable", "request_timeout"}

// callSlack calls a Web API method: a POST with a JSON body, or a GET when
// body is nil. Slack answers 200 with ok: false for most errors; their error
// string becomes the failure's type. A 429 waits as long as Retry-After asks.
func (a *Activities) callSlack(ctx context.Context, method string, body, out any) error {
	sl := a.Slack
	if sl == nil || sl.Token == "" {
		return notConfigured("SLACK_BOT_TOKEN")
	}
	defer keepAlive(ctx, "waiting for Slack: "+method)()
	httpMethod, payload := http.MethodGet, []byte(nil)
	if body != nil {
		var err error
		if payload, err = jsonBody(body); err != nil {
			return err
		}
		httpMethod = http.MethodPost
	}
	resp, err := apiCall(ctx, sl.HTTP, httpMethod, sl.URL+"/"+method, bearer(sl.Token), "application/json; charset=utf-8", payload)
	if err != nil {
		return err
	}
	var res struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	_ = resp.decodeInto("Slack", &res)
	if resp.Status == http.StatusTooManyRequests {
		delay, _ := retryAfter(resp.Header.Get("Retry-After"), time.Now())
		return connectorError("Slack "+method+" was rate limited", "ratelimited", true, delay, nil)
	}
	if resp.Status >= 500 {
		return connectorError(fmt.Sprintf("Slack %s returned %d", method, resp.Status), "service_unavailable", true, 0, nil)
	}
	if !res.OK {
		code := res.Error
		if code == "" {
			code = "slack_error"
		}
		return connectorError("Slack "+method+": "+code, code, slices.Contains(retryableSlackErrors, code), 0, nil)
	}
	return resp.decodeInto("Slack", out)
}
