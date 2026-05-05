package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"sonic-siphon/internal/api"
	"sonic-siphon/internal/web"
)

func main() {
	var (
		addr      = flag.String("addr", ":5000", "listen address")
		tempDir   = flag.String("temp-dir", "/temp", "directory for in-progress downloads")
		outputDir = flag.String("output-dir", "/output", "directory for curated files")
		apiOnly   = flag.Bool("api-only", false, "disable the web frontend; serve API only")
	)
	flag.Parse()

	if err := os.MkdirAll(*tempDir, 0755); err != nil {
		log.Fatalf("Failed to create temp directory: %v", err)
	}
	if err := os.MkdirAll(*outputDir, 0755); err != nil {
		log.Fatalf("Failed to create output directory: %v", err)
	}

	authCfg := api.AuthConfig{
		Username:     os.Getenv("ADMIN_USERNAME"),
		Password:     os.Getenv("ADMIN_PASSWORD"),
		PublicDocs:   parseBoolEnv("PUBLIC_DOCS", true),
		SecureCookie: parseBoolEnv("COOKIE_SECURE", false),
	}
	store := api.NewSessionStore(24 * time.Hour)
	loginLimiter := api.NewRateLimiter(5, 15*time.Minute)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	store.StartJanitor(ctx, 5*time.Minute)
	loginLimiter.StartJanitor(ctx, 5*time.Minute)

	gin.SetMode(gin.ReleaseMode)
	r := gin.Default()

	// Restrict which client IPs can be inferred from X-Forwarded-For. By default
	// only loopback is trusted, so a client can't spoof its IP and bypass the
	// per-IP login rate limiter. Override via TRUSTED_PROXIES (comma-separated)
	// when running behind a reverse proxy.
	trusted := []string{"127.0.0.1", "::1"}
	if env := strings.TrimSpace(os.Getenv("TRUSTED_PROXIES")); env != "" {
		trusted = splitAndTrim(env, ",")
	}
	if err := r.SetTrustedProxies(trusted); err != nil {
		log.Fatalf("invalid TRUSTED_PROXIES: %v", err)
	}

	r.Use(api.SecurityHeaders())
	r.Use(api.AuthMiddleware(authCfg, store))

	api.New(r, api.Config{TempDir: *tempDir, OutputDir: *outputDir})

	if !*apiOnly {
		web.Mount(r, authCfg, store, loginLimiter)
	}

	log.Printf("Starting server on %s (api-only=%v auth=%v public-docs=%v secure-cookie=%v trusted-proxies=%v)",
		*addr, *apiOnly, authCfg.Enabled(), authCfg.PublicDocs, authCfg.SecureCookie, trusted)
	if err := r.Run(*addr); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

// parseBoolEnv reads an env var and parses common truthy/falsy values.
// Returns def if unset or unrecognized.
func parseBoolEnv(name string, def bool) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	switch v {
	case "":
		return def
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return def
}

// splitAndTrim splits s on sep and trims whitespace from each element,
// dropping empty entries.
func splitAndTrim(s, sep string) []string {
	parts := strings.Split(s, sep)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
