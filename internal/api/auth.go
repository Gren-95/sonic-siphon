package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// SessionCookieName is the cookie carrying the session ID.
const SessionCookieName = "sonic_session"

// AuthConfig holds the static admin credentials and feature flags.
// Auth is enabled iff Username and Password are both non-empty.
type AuthConfig struct {
	Username     string
	Password     string
	PublicDocs   bool // when true, /api/v1/docs + /api/v1/openapi.json + /api/v1/schemas/* skip auth
	SecureCookie bool // set the Secure flag on the session cookie (require HTTPS)
}

func (c AuthConfig) Enabled() bool {
	return c.Username != "" && c.Password != ""
}

// CheckCredentials returns true if the given username and password match
// the configured admin (constant-time compare to avoid timing leaks).
func (c AuthConfig) CheckCredentials(user, pass string) bool {
	if !c.Enabled() {
		return false
	}
	u := subtle.ConstantTimeCompare([]byte(user), []byte(c.Username))
	p := subtle.ConstantTimeCompare([]byte(pass), []byte(c.Password))
	return u == 1 && p == 1
}

// SessionStore is an in-memory session ID -> expiry map.
// Sessions are lost on restart; that's acceptable for a single-user tool.
type SessionStore struct {
	mu       sync.RWMutex
	sessions map[string]time.Time
	ttl      time.Duration
}

func NewSessionStore(ttl time.Duration) *SessionStore {
	return &SessionStore{
		sessions: make(map[string]time.Time),
		ttl:      ttl,
	}
}

// Create generates a new cryptographically random session ID and stores it
// with an expiry of now+ttl. Returns the new ID.
func (s *SessionStore) Create() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	id := hex.EncodeToString(b)
	s.mu.Lock()
	s.sessions[id] = time.Now().Add(s.ttl)
	s.mu.Unlock()
	return id, nil
}

// Validate returns true if id is a known unexpired session.
// Expired sessions are evicted as a side effect.
func (s *SessionStore) Validate(id string) bool {
	if id == "" {
		return false
	}
	s.mu.RLock()
	expires, ok := s.sessions[id]
	s.mu.RUnlock()
	if !ok {
		return false
	}
	if time.Now().After(expires) {
		s.Delete(id)
		return false
	}
	return true
}

func (s *SessionStore) Delete(id string) {
	s.mu.Lock()
	delete(s.sessions, id)
	s.mu.Unlock()
}

// TTL returns the configured session lifetime, useful for setting cookie Max-Age.
func (s *SessionStore) TTL() time.Duration { return s.ttl }

// Cleanup removes all expired sessions and returns the number deleted.
// Pure / synchronous — the janitor goroutine calls this on a tick.
func (s *SessionStore) Cleanup() int {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	deleted := 0
	for id, expires := range s.sessions {
		if now.After(expires) {
			delete(s.sessions, id)
			deleted++
		}
	}
	return deleted
}

// StartJanitor runs Cleanup() on a ticker until ctx is cancelled.
// Non-blocking: spawns a goroutine and returns immediately.
func (s *SessionStore) StartJanitor(ctx context.Context, interval time.Duration) {
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.Cleanup()
			}
		}
	}()
}

// IsPublicPath reports whether a request path bypasses auth.
// publicDocs is the AuthConfig flag: when true, OpenAPI/docs/schemas are open.
func IsPublicPath(path string, publicDocs bool) bool {
	switch path {
	case "/login", "/logout", "/favicon.svg", "/manifest.json":
		return true
	case "/api/v1/health":
		return true
	}
	if strings.HasPrefix(path, "/static/") {
		return true
	}
	if publicDocs {
		switch path {
		case "/api/v1/openapi.json", "/api/v1/openapi.yaml", "/api/v1/openapi", "/api/v1/docs":
			return true
		}
		if strings.HasPrefix(path, "/api/v1/schemas/") {
			return true
		}
	}
	return false
}

// AuthMiddleware enforces session-based auth for protected routes.
// When AuthConfig.Enabled() is false, this is a passthrough.
// Browser GETs that want HTML are redirected to /login; everything else
// (XHR, API calls) gets a 401 JSON response.
func AuthMiddleware(cfg AuthConfig, store *SessionStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !cfg.Enabled() {
			c.Next()
			return
		}
		if IsPublicPath(c.Request.URL.Path, cfg.PublicDocs) {
			c.Next()
			return
		}
		cookie, _ := c.Cookie(SessionCookieName)
		if store.Validate(cookie) {
			c.Next()
			return
		}
		// API requests always get a JSON 401 — they're machine-driven, never browser nav.
		// Other GETs to non-API paths redirect to the login page.
		if !strings.HasPrefix(c.Request.URL.Path, "/api/") && c.Request.Method == http.MethodGet {
			c.Redirect(http.StatusFound, "/login")
			c.Abort()
			return
		}
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
	}
}
