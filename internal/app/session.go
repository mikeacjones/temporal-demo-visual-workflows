package app

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"regexp"
	"strings"
)

const sessionCookie = "vf_session"

// user is who a request is from. Key scopes everything: every Workflow ID the
// user touches starts with their prefix, which is how the API enforces
// isolation. A request can only name Workflows under its own prefix.
type user struct {
	Key      string
	Name     string
	SignedIn bool
}

func (s *Server) identify(w http.ResponseWriter, r *http.Request) user {
	// Behind the catalog ingress, the platform's SSO gate supplies the
	// subject. Trusting that header is opt-in, because anywhere else a client
	// could simply send it.
	if s.cfg.TrustAuthHeaders {
		if subject := r.Header.Get("X-Temporal-Auth-Subject"); subject != "" {
			sum := sha256.Sum256([]byte(subject))
			name := r.Header.Get("X-Temporal-Auth-Email")
			if name == "" {
				name = "Signed-in user"
			}
			return user{Key: hex.EncodeToString(sum[:])[:12], Name: name, SignedIn: true}
		}
	}
	if c, err := r.Cookie(sessionCookie); err == nil {
		if key, ok := s.verify(c.Value); ok {
			return user{Key: key, Name: "You"}
		}
	}
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	key := hex.EncodeToString(b)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    s.sign(key),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
		MaxAge:   30 * 24 * 3600,
	})
	return user{Key: key, Name: "You"}
}

// sign returns value.signature, so a client cannot pick another user's key.
func (s *Server) sign(value string) string {
	mac := hmac.New(sha256.New, s.cfg.SessionSecret)
	mac.Write([]byte(value))
	return value + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *Server) verify(token string) (string, bool) {
	value, _, ok := strings.Cut(token, ".")
	if !ok || !hmac.Equal([]byte(s.sign(value)), []byte(token)) {
		return "", false
	}
	return value, true
}

// prefix scopes Workflow IDs to the task queue (one per isolated workspace,
// which share a namespace) and to the user.
func (s *Server) prefix(userKey string) string {
	return "vflow-" + s.cfg.TaskQueue + "-" + userKey + "-"
}

func (s *Server) libraryID(userKey string) string { return s.prefix(userKey) + "library" }

func (s *Server) galleryID() string { return "vflow-" + s.cfg.TaskQueue + "-gallery" }

func (s *Server) scheduleID(userKey, flowID string) string {
	return s.prefix(userKey) + flowID + "-schedule"
}

// Run IDs carry the flow ID followed by "."; flow IDs can't contain ".", so
// one flow's runs never match another's prefix.
func (s *Server) flowRunPrefix(userKey, flowID string) string {
	return s.prefix(userKey) + flowID + "."
}

var unsafeKeyChars = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

// keyedRunID is the one Workflow ID for a keyed flow's key, so starting it
// twice reaches the same run.
func (s *Server) keyedRunID(userKey, flowID, key string) string {
	k := unsafeKeyChars.ReplaceAllString(key, "_")
	if len(k) > 64 {
		k = k[:64]
	}
	return s.flowRunPrefix(userKey, flowID) + "key-" + k
}

// webhookToken names a user's flow without exposing anything guessable.
func (s *Server) webhookToken(userKey, flowID string) string {
	return s.sign(base64.RawURLEncoding.EncodeToString([]byte(userKey + "|" + flowID)))
}

func (s *Server) parseWebhookToken(token string) (userKey, flowID string, ok bool) {
	value, ok := s.verify(token)
	if !ok {
		return "", "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return "", "", false
	}
	userKey, flowID, ok = strings.Cut(string(raw), "|")
	return userKey, flowID, ok
}

func randomSuffix() string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
