package app

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
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
	// Behind the catalog ingress, the platform's SSO gate supplies who is
	// signed in. A signed-in user is keyed by their email, so they get the
	// same library, flows and runs from any browser. Trusting those headers
	// is opt-in, because anywhere else a client could simply send them.
	if s.cfg.TrustAuthHeaders {
		if email := signedInEmail(r); email != "" {
			return user{Key: userKey(email), Name: email, SignedIn: true}
		}
	}
	if c, err := r.Cookie(sessionCookie); err == nil {
		if key, ok := s.verify(c.Value); ok {
			return user{Key: key, Name: "You"}
		}
	}
	if s.cfg.TrustAuthHeaders {
		// Behind the gate every request should be signed in; a new anonymous
		// session there means the identity headers didn't arrive.
		s.log.Warn("request without a signed-in identity; starting an anonymous session",
			"path", r.URL.Path, "authHeaders", authHeaderNames(r))
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

// signedInEmail is the signed-in user's email, normalized: from the gate's
// email header, or else from its token's email claim.
func signedInEmail(r *http.Request) string {
	email := r.Header.Get("X-Temporal-Auth-Email")
	if email == "" {
		email = tokenEmail(r.Header.Get("X-Temporal-Auth-JWT"))
	}
	return strings.ToLower(strings.TrimSpace(email))
}

// tokenEmail reads the email claim from a JWT's payload. The gate has
// already verified the token, and the ingress strips any copy a client sends.
func tokenEmail(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		Email string `json:"email"`
	}
	if json.Unmarshal(payload, &claims) != nil {
		return ""
	}
	return claims.Email
}

// userKey is a signed-in user's key: the same for an email everywhere, and
// short enough to sit in every Workflow ID they own.
func userKey(email string) string {
	sum := sha256.Sum256([]byte(email))
	return hex.EncodeToString(sum[:])[:12]
}

// authHeaderNames lists the gate's identity headers present on a request,
// by name only, for diagnosing a missing identity.
func authHeaderNames(r *http.Request) []string {
	var names []string
	for _, h := range []string{"X-Temporal-Auth-Subject", "X-Temporal-Auth-Email", "X-Temporal-Auth-JWT"} {
		if r.Header.Get(h) != "" {
			names = append(names, h)
		}
	}
	return names
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

// libraryID names a user's library. It is scoped to the demo's base task
// queue, not a crashable workspace's, so a workspace opens the user's own
// library.
func (s *Server) libraryID(userKey string) string {
	return "vflow-" + s.cfg.LibraryScope + "-" + userKey + "-library"
}

func (s *Server) galleryID() string { return "vflow-" + s.cfg.LibraryScope + "-gallery" }

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
