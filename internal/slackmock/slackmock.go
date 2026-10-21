// Package slackmock is a small, in-memory Slack workspace for the sandbox and
// the tests. It speaks the Web API methods the Slack connector uses, with the
// same requests and answers: HTTP 200 with {"ok": false, "error": "..."} for
// most errors, and 429 with Retry-After when a channel gets more than a few
// messages a second.
//
//   - chat.postMessage, chat.update, chat.delete, reactions.add,
//     users.lookupByEmail;
//   - channels by #name or ID. The bot is in every channel but #random, so
//     posting there fails with not_in_channel.
//
// GET /messages (or /messages/{channel}) lists what was posted, newest
// first, so a presenter can show it.
package slackmock

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
)

// Token is the demo bot's token. The worker uses it by default.
const Token = "xoxb-visual-workflows-demo"

// Latency makes calls take about as long as Slack's.
var Latency = 100 * time.Millisecond

// PerChannelBurst is how many messages a channel takes in a second before
// answering 429.
var PerChannelBurst = 5

type channel struct {
	id, name string
	member   bool
}

var channels = []channel{
	{"C04GENERAL", "general", true}, {"C04SALES01", "sales", true}, {"C04INCIDNT", "incidents", true},
	{"C04OPS0001", "ops", true}, {"C04SUPPORT", "support", true}, {"C04PAYMNTS", "payments", true},
	{"C04RANDOM1", "random", false},
}

var users = []map[string]any{
	{"id": "U04RILEY01", "name": "riley", "real_name": "Riley Chen", "email": "riley@example.com"},
	{"id": "U04DANA001", "name": "dana", "real_name": "Dana Ortiz", "email": "dana@example.com"},
	{"id": "U04SAM0001", "name": "sam", "real_name": "Sam Patel", "email": "sam@example.com"},
}

// Message is a message as posted.
type Message struct {
	Channel   string   `json:"channel"`
	Name      string   `json:"channelName"`
	TS        string   `json:"ts"`
	ThreadTS  string   `json:"threadTs,omitempty"`
	Text      string   `json:"text"`
	Blocks    any      `json:"blocks,omitempty"`
	Reactions []string `json:"reactions,omitempty"`
	Edited    bool     `json:"edited,omitempty"`
}

// Workspace is one in-memory workspace.
type Workspace struct {
	logger *slog.Logger

	mu       sync.Mutex
	messages []*Message
	recent   map[string][]time.Time // channel ID -> recent post times
	seq      int
}

// New returns an empty workspace.
func New(logger *slog.Logger) *Workspace {
	return &Workspace{logger: logger, recent: map[string][]time.Time{}}
}

// Handler serves the Web API under prefix/api, and the message list.
func (ws *Workspace) Handler(prefix string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/{method}", ws.api)
	mux.HandleFunc("GET /messages", ws.list)
	mux.HandleFunc("GET /messages/{channel}", ws.list)
	return http.StripPrefix(prefix, mux)
}

func (ws *Workspace) api(w http.ResponseWriter, r *http.Request) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	args := readArgs(r)
	if !ok {
		token, _ = args["token"].(string)
	}
	switch {
	case token == "":
		fail(w, "not_authed")
		return
	case token != Token:
		fail(w, "invalid_auth")
		return
	}
	time.Sleep(Latency)
	str := func(k string) string { s, _ := args[k].(string); return s }

	switch r.PathValue("method") {
	case "chat.postMessage":
		ch, errCode := find(str("channel"))
		if errCode == "" && !ch.member {
			errCode = "not_in_channel"
		}
		if errCode == "" && str("text") == "" && args["blocks"] == nil {
			errCode = "no_text"
		}
		if errCode != "" {
			fail(w, errCode)
			return
		}
		ws.mu.Lock()
		now := time.Now()
		recent := slices.DeleteFunc(ws.recent[ch.id], func(t time.Time) bool { return now.Sub(t) > time.Second })
		if len(recent) >= PerChannelBurst {
			ws.mu.Unlock()
			ws.logger.Warn("slack: rate limited", "channel", ch.name)
			w.Header().Set("Retry-After", "1")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "ratelimited"})
			return
		}
		ws.recent[ch.id] = append(recent, now)
		ws.seq++
		m := &Message{Channel: ch.id, Name: ch.name, TS: fmt.Sprintf("%d.%06d", now.Unix(), ws.seq), ThreadTS: str("thread_ts"), Text: str("text"), Blocks: args["blocks"]}
		ws.messages = append(ws.messages, m)
		ws.mu.Unlock()
		ws.logger.Info("slack: message posted", "channel", "#"+ch.name, "text", m.Text)
		reply(w, map[string]any{"channel": ch.id, "ts": m.TS, "message": map[string]any{"type": "message", "text": m.Text, "ts": m.TS, "bot_id": "B04VISUALWF"}})

	case "chat.update", "chat.delete", "reactions.add":
		ch, errCode := find(str("channel"))
		ts := str("ts")
		if r.PathValue("method") == "reactions.add" {
			ts = str("timestamp")
		}
		ws.mu.Lock()
		defer ws.mu.Unlock()
		i := slices.IndexFunc(ws.messages, func(m *Message) bool { return m.Channel == ch.id && m.TS == ts })
		if errCode == "" && i < 0 {
			errCode = "message_not_found"
		}
		if errCode != "" {
			fail(w, errCode)
			return
		}
		m := ws.messages[i]
		switch r.PathValue("method") {
		case "chat.update":
			m.Text, m.Edited = str("text"), true
			if args["blocks"] != nil {
				m.Blocks = args["blocks"]
			}
			ws.logger.Info("slack: message updated", "channel", "#"+ch.name, "text", m.Text)
			reply(w, map[string]any{"channel": ch.id, "ts": ts, "text": m.Text})
		case "chat.delete":
			ws.messages = slices.Delete(ws.messages, i, i+1)
			ws.logger.Info("slack: message deleted", "channel", "#"+ch.name, "ts", ts)
			reply(w, map[string]any{"channel": ch.id, "ts": ts})
		default:
			name := strings.Trim(str("name"), ":")
			if slices.Contains(m.Reactions, name) {
				fail(w, "already_reacted")
				return
			}
			m.Reactions = append(m.Reactions, name)
			ws.logger.Info("slack: reaction added", "channel", "#"+ch.name, "name", name)
			reply(w, nil)
		}

	case "users.lookupByEmail":
		i := slices.IndexFunc(users, func(u map[string]any) bool { return strings.EqualFold(u["email"].(string), str("email")) })
		if i < 0 {
			fail(w, "users_not_found")
			return
		}
		u := users[i]
		reply(w, map[string]any{"user": map[string]any{"id": u["id"], "name": u["name"], "real_name": u["real_name"], "profile": map[string]any{"email": u["email"], "real_name": u["real_name"]}}})

	default:
		fail(w, "unknown_method")
	}
}

func (ws *Workspace) list(w http.ResponseWriter, r *http.Request) {
	want := strings.TrimPrefix(r.PathValue("channel"), "#")
	ws.mu.Lock()
	out := []*Message{}
	for i := len(ws.messages) - 1; i >= 0; i-- {
		if m := ws.messages[i]; want == "" || m.Name == want || m.Channel == want {
			out = append(out, m)
		}
	}
	ws.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// Messages returns what was posted, oldest first, for tests.
func (ws *Workspace) Messages() []Message {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	out := make([]Message, 0, len(ws.messages))
	for _, m := range ws.messages {
		out = append(out, *m)
	}
	return out
}

// find resolves a channel by ID, name or #name.
func find(ref string) (channel, string) {
	name := strings.TrimPrefix(ref, "#")
	i := slices.IndexFunc(channels, func(c channel) bool { return c.id == ref || c.name == name })
	if i < 0 {
		return channel{}, "channel_not_found"
	}
	return channels[i], ""
}

// readArgs reads a method's arguments from a JSON body, a form, or the query
// string, as Slack accepts all three.
func readArgs(r *http.Request) map[string]any {
	args := map[string]any{}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		_ = json.NewDecoder(r.Body).Decode(&args)
		return args
	}
	_ = r.ParseForm()
	for k, v := range r.Form {
		args[k] = v[0]
	}
	if s, ok := args["blocks"].(string); ok {
		var blocks any
		if json.Unmarshal([]byte(s), &blocks) == nil {
			args["blocks"] = blocks
		}
	}
	return args
}

func reply(w http.ResponseWriter, fields map[string]any) {
	body := map[string]any{"ok": true}
	for k, v := range fields {
		body[k] = v
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

func fail(w http.ResponseWriter, code string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": code})
}
