package api

import (
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
)

// SecurityHeaders returns a gin middleware that adds the baseline OWASP
// recommended response headers. The CSP allows inline scripts/styles because
// the bundled index.html embeds both; tightening that further would require
// extracting all inline content into separate files.
//
// Cache-Control: no-store is applied to everything except cacheable static
// assets (CSS, icons, manifest) so that pages and API responses — which can
// leak file listings, session state, or JSON content — are not stored by
// browsers, forward proxies, or CDNs.
func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
		h.Set("Content-Security-Policy",
			"default-src 'self'; "+
				"img-src 'self' data: blob:; "+
				"media-src 'self' blob:; "+
				"style-src 'self' 'unsafe-inline'; "+
				"script-src 'self' 'unsafe-inline'; "+
				"frame-ancestors 'none'")
		if !isCacheableStatic(c.Request.URL.Path) {
			h.Set("Cache-Control", "no-store")
		}
		c.Next()
	}
}

// isCacheableStatic reports whether the response for path is safe to cache.
// Anything else gets Cache-Control: no-store.
func isCacheableStatic(path string) bool {
	if strings.HasPrefix(path, "/static/") {
		return true
	}
	return path == "/favicon.svg" || path == "/manifest.json"
}

// allowedSourceHosts is the SSRF allowlist for URLs accepted by /preview and
// /downloads. Hosts are matched case-insensitively against the URL host
// (with port stripped); subdomains are accepted via suffix match.
var allowedSourceHosts = []string{
	"youtube.com",
	"youtu.be",
	"music.youtube.com",
}

// IsAllowedSourceURL reports whether rawURL is something we'll let yt-dlp
// fetch. Without this, the server is a SSRF gadget: any caller could ask
// it to GET internal endpoints (http://localhost:5432, the cloud metadata
// service, etc.) via yt-dlp's URL extraction step.
func IsAllowedSourceURL(rawURL string) bool {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return false
	}
	for _, allowed := range allowedSourceHosts {
		if host == allowed || strings.HasSuffix(host, "."+allowed) {
			return true
		}
	}
	return false
}
