package web

// Real end-to-end security tests for the login/logout/session-cookie surface.
// Each test spins up a gin engine + the real auth middleware + Mount(), then
// drives it with actual HTTP requests via httptest.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"sonic-siphon/internal/api"
)

func newServer(t *testing.T, cfg api.AuthConfig, store *api.SessionStore, limiter *api.RateLimiter) (*httptest.Server, *gin.Engine) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(api.AuthMiddleware(cfg, store))
	Mount(r, cfg, store, limiter)
	// A protected endpoint we can exercise to prove the cookie is honoured.
	r.GET("/_probe", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv, r
}

func postForm(t *testing.T, client *http.Client, url, user, pass string) *http.Response {
	t.Helper()
	form := make([]string, 0, 2)
	form = append(form, "username="+user, "password="+pass)
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(strings.Join(form, "&")))
	if err != nil {
		t.Fatalf("build req: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	return resp
}

// noRedirectClient stops the http client from following 3xx so we can inspect
// Location and Set-Cookie headers directly.
func noRedirectClient() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// TestIntegration_CookieSecureFlag verifies the Secure attribute is added
// to the session cookie when SecureCookie=true (and only then).
func TestIntegration_CookieSecureFlag(t *testing.T) {
	for _, tc := range []struct {
		name   string
		secure bool
	}{
		{name: "secure true", secure: true},
		{name: "secure false", secure: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := api.AuthConfig{Username: "u", Password: "p", SecureCookie: tc.secure}
			srv, _ := newServer(t, cfg, api.NewSessionStore(time.Hour), nil)

			resp := postForm(t, noRedirectClient(), srv.URL+"/login", "u", "p")
			defer resp.Body.Close()

			cookie := pickCookie(resp, api.SessionCookieName)
			if cookie == nil {
				t.Fatal("no session cookie set")
			}
			if cookie.Secure != tc.secure {
				t.Errorf("Secure flag = %v, want %v", cookie.Secure, tc.secure)
			}
			if !cookie.HttpOnly {
				t.Error("HttpOnly missing — must always be set")
			}
			if cookie.SameSite != http.SameSiteLaxMode {
				t.Errorf("SameSite = %v, want Lax", cookie.SameSite)
			}
		})
	}
}

// TestIntegration_LoginFlowRoundtrip drives the whole cookie lifecycle:
// wrong creds -> right creds -> protected access -> logout -> stale cookie rejected.
func TestIntegration_LoginFlowRoundtrip(t *testing.T) {
	cfg := api.AuthConfig{Username: "admin", Password: "s3cret"}
	store := api.NewSessionStore(time.Hour)
	srv, _ := newServer(t, cfg, store, nil)
	cli := noRedirectClient()

	// 1. Wrong creds -> 302 to /login?err=1, no cookie.
	resp := postForm(t, cli, srv.URL+"/login", "admin", "WRONG")
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("wrong creds status = %d, want 302", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/login?err=1" {
		t.Errorf("wrong creds Location = %q, want /login?err=1", loc)
	}
	if c := pickCookie(resp, api.SessionCookieName); c != nil && c.Value != "" {
		t.Error("wrong creds should not set a session cookie")
	}

	// 2. Right creds -> 302 to /, cookie set.
	resp = postForm(t, cli, srv.URL+"/login", "admin", "s3cret")
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("right creds status = %d, want 302", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/" {
		t.Errorf("right creds Location = %q, want /", loc)
	}
	cookie := pickCookie(resp, api.SessionCookieName)
	if cookie == nil || cookie.Value == "" {
		t.Fatal("right creds did not set a session cookie")
	}

	// 3. Protected endpoint with the cookie -> 200.
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/_probe", nil)
	req.AddCookie(cookie)
	r2, err := cli.Do(req)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	r2.Body.Close()
	if r2.StatusCode != http.StatusOK {
		t.Errorf("protected GET with cookie: status = %d, want 200", r2.StatusCode)
	}

	// 4. POST /logout with the cookie -> 302, cookie cleared, store invalidates the ID.
	req, _ = http.NewRequest(http.MethodPost, srv.URL+"/logout", nil)
	req.AddCookie(cookie)
	r3, err := cli.Do(req)
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	r3.Body.Close()
	if r3.StatusCode != http.StatusFound {
		t.Errorf("logout status = %d, want 302", r3.StatusCode)
	}
	cleared := pickCookie(r3, api.SessionCookieName)
	if cleared == nil || cleared.MaxAge >= 0 {
		t.Error("logout did not emit a Max-Age<0 cookie to clear the session")
	}
	if store.Validate(cookie.Value) {
		t.Error("session still valid in store after logout")
	}

	// 5. Re-attempt protected GET with the same (now-invalidated) cookie -> 401 JSON.
	req, _ = http.NewRequest(http.MethodGet, srv.URL+"/api/v1/files", nil)
	req.AddCookie(cookie)
	req.Header.Set("Accept", "application/json")
	r4, err := cli.Do(req)
	if err != nil {
		t.Fatalf("post-logout probe: %v", err)
	}
	r4.Body.Close()
	if r4.StatusCode != http.StatusUnauthorized {
		t.Errorf("post-logout API status = %d, want 401", r4.StatusCode)
	}
}

// TestIntegration_LoginRateLimitTriggers429 verifies the per-IP login limiter
// kicks in after the configured number of failed attempts and returns
// 429 + Retry-After to programmatic clients (or 302 to /login?err=2 to browsers).
func TestIntegration_LoginRateLimitTriggers429(t *testing.T) {
	cfg := api.AuthConfig{Username: "admin", Password: "s3cret"}
	store := api.NewSessionStore(time.Hour)
	limiter := api.NewRateLimiter(3, time.Minute)
	srv, _ := newServer(t, cfg, store, limiter)
	cli := noRedirectClient()

	// First N=3 wrong attempts: 302 to /login?err=1, not rate-limited yet.
	for i := 1; i <= 3; i++ {
		resp := postForm(t, cli, srv.URL+"/login", "admin", "WRONG")
		resp.Body.Close()
		if resp.StatusCode != http.StatusFound {
			t.Fatalf("attempt %d: status = %d, want 302", i, resp.StatusCode)
		}
		if loc := resp.Header.Get("Location"); loc != "/login?err=1" {
			t.Errorf("attempt %d: Location = %q, want /login?err=1", i, loc)
		}
	}

	// 4th attempt with no Accept header: programmatic-style — 429 + Retry-After header + JSON body.
	resp := postForm(t, cli, srv.URL+"/login", "admin", "WRONG")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("rate-limited attempt: status = %d, want 429", resp.StatusCode)
	}
	ra := resp.Header.Get("Retry-After")
	if ra == "" {
		t.Error("Retry-After header missing on 429")
	} else if n, err := strconv.Atoi(ra); err != nil || n <= 0 {
		t.Errorf("Retry-After = %q, want positive integer seconds", ra)
	}
	if !strings.Contains(string(body), "too many failed login attempts") {
		t.Errorf("body = %q, want 'too many failed login attempts'", body)
	}

	// Same situation with Accept: text/html (browser nav) -> 302 to /login?err=2 + Retry-After.
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/login",
		strings.NewReader("username=admin&password=WRONG"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	resp2, err := cli.Do(req)
	if err != nil {
		t.Fatalf("browser-style POST: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusFound {
		t.Errorf("browser rate-limited status = %d, want 302", resp2.StatusCode)
	}
	if loc := resp2.Header.Get("Location"); loc != "/login?err=2" {
		t.Errorf("browser rate-limited Location = %q, want /login?err=2", loc)
	}
	if resp2.Header.Get("Retry-After") == "" {
		t.Error("Retry-After header missing on browser-style 302")
	}
}

// TestIntegration_RateLimiterDoesNotPenalizeSuccess confirms a flood of
// successful logins doesn't burn the failure quota.
func TestIntegration_RateLimiterDoesNotPenalizeSuccess(t *testing.T) {
	cfg := api.AuthConfig{Username: "admin", Password: "s3cret"}
	store := api.NewSessionStore(time.Hour)
	limiter := api.NewRateLimiter(2, time.Minute)
	srv, _ := newServer(t, cfg, store, limiter)
	cli := noRedirectClient()

	// 5 successful logins — none should consume quota.
	for i := 0; i < 5; i++ {
		resp := postForm(t, cli, srv.URL+"/login", "admin", "s3cret")
		resp.Body.Close()
		if resp.StatusCode != http.StatusFound {
			t.Fatalf("success #%d: status = %d", i+1, resp.StatusCode)
		}
	}
	// Two failed attempts allowed.
	for i := 0; i < 2; i++ {
		resp := postForm(t, cli, srv.URL+"/login", "admin", "WRONG")
		resp.Body.Close()
		if resp.StatusCode != http.StatusFound {
			t.Fatalf("bad #%d: status = %d", i+1, resp.StatusCode)
		}
	}
	// Third bad attempt blocked.
	resp := postForm(t, cli, srv.URL+"/login", "admin", "WRONG")
	resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("post-limit status = %d, want 429", resp.StatusCode)
	}
}

func pickCookie(resp *http.Response, name string) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// silence unused import check if helpers shrink later
var _ = url.Values{}
