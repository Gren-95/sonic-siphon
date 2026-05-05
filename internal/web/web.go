package web

import (
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"sonic-siphon/internal/api"
)

// Mount registers the static frontend, login/logout endpoints, and the index page.
// Auth enforcement is handled by api.AuthMiddleware (registered globally on the engine);
// this package just owns the user-facing entry points.
//
// loginLimiter throttles failed login attempts per client IP. Pass nil to disable.
func Mount(r *gin.Engine, authCfg api.AuthConfig, store *api.SessionStore, loginLimiter *api.RateLimiter) {
	r.Static("/static", "./static")
	r.StaticFile("/favicon.svg", "./static/favicon.svg")
	r.StaticFile("/manifest.json", "./static/manifest.json")

	r.GET("/login", func(c *gin.Context) {
		c.File("./templates/login.html")
	})
	r.POST("/login", loginHandler(authCfg, store, loginLimiter))
	r.POST("/logout", logoutHandler(authCfg, store))

	r.GET("/", func(c *gin.Context) {
		c.File("./templates/index.html")
	})
}

func loginHandler(cfg api.AuthConfig, store *api.SessionStore, limiter *api.RateLimiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !cfg.Enabled() {
			// Auth not configured — nothing to log in to. Send them home.
			c.Redirect(http.StatusFound, "/")
			return
		}

		ip := c.ClientIP()
		if limiter != nil && limiter.CountFailures(ip) >= limiter.Limit() {
			log.Printf("[security] login rate-limited ip=%s", ip)
			respondRateLimited(c, limiter.RetryAfter(ip))
			return
		}

		username := c.PostForm("username")
		password := c.PostForm("password")
		if !cfg.CheckCredentials(username, password) {
			if limiter != nil {
				limiter.RecordFailure(ip)
			}
			log.Printf("[security] login failed ip=%s user=%q", ip, username)
			c.Redirect(http.StatusFound, "/login?err=1")
			return
		}

		sid, err := store.Create()
		if err != nil {
			log.Printf("[security] session create failed ip=%s user=%q err=%v", ip, username, err)
			c.String(http.StatusInternalServerError, "session creation failed")
			return
		}
		log.Printf("[security] login ok ip=%s user=%q", ip, username)
		// Cookie flags: HttpOnly + SameSite=Lax always. Secure is gated by config —
		// off by default so it works on plain http://localhost; flip COOKIE_SECURE=true
		// when running behind TLS or a TLS-terminating reverse proxy.
		c.SetSameSite(http.SameSiteLaxMode)
		c.SetCookie(api.SessionCookieName, sid, int(store.TTL().Seconds()), "/", "", cfg.SecureCookie, true)
		c.Redirect(http.StatusFound, "/")
	}
}

func logoutHandler(cfg api.AuthConfig, store *api.SessionStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cookie, err := c.Cookie(api.SessionCookieName); err == nil {
			store.Delete(cookie)
		}
		c.SetSameSite(http.SameSiteLaxMode)
		c.SetCookie(api.SessionCookieName, "", -1, "/", "", cfg.SecureCookie, true)
		log.Printf("[security] logout ip=%s", c.ClientIP())
		c.Redirect(http.StatusFound, "/login")
	}
}

// respondRateLimited returns a 429 JSON to programmatic clients, or redirects
// browser POSTs to /login?err=2 so the human sees a friendly message.
// Always emits a Retry-After header (seconds, rounded up) so well-behaved
// scripts can back off.
func respondRateLimited(c *gin.Context, retryAfter time.Duration) {
	if retryAfter < time.Second {
		retryAfter = time.Second
	}
	secs := int((retryAfter + time.Second - 1) / time.Second)
	c.Header("Retry-After", strconv.Itoa(secs))

	if strings.Contains(c.GetHeader("Accept"), "text/html") {
		c.Redirect(http.StatusFound, "/login?err=2")
		return
	}
	c.JSON(http.StatusTooManyRequests, gin.H{
		"error":           "too many failed login attempts",
		"retry_after_sec": secs,
	})
}
