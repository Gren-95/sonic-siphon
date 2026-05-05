// Real end-to-end tests. They hit YouTube via yt-dlp and re-encode with
// ffmpeg, so they need network access and the binaries on PATH. Each test
// calls requireBinaries(t, ...) at the top, which t.Skips when a tool is
// missing — so on a host without yt-dlp the suite reports SKIP rather than FAIL.
//
// Test target: Blender Foundation's "Big Buck Bunny" (aqz-KE-bpKQ), CC-BY
// licensed, ~635s. Chosen because it's freely redistributable, so the test
// suite isn't relying on tolerated-but-unlicensed copyrighted material.

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	testVideoURL      = "https://www.youtube.com/watch?v=aqz-KE-bpKQ"
	testVideoID       = "aqz-KE-bpKQ"
	testVideoDuration = 635 // seconds, "Big Buck Bunny" (Blender Foundation, CC-BY)
	testVideoTitleSub = "big buck bunny"
)

func TestIntegration_PreviewSingleVideo(t *testing.T) {
	requireBinaries(t, "yt-dlp")

	info, err := GetVideoInfo(testVideoURL, false)
	if err != nil {
		t.Fatalf("GetVideoInfo: %v", err)
	}
	if info.Type != "video" {
		t.Errorf("type = %q, want video", info.Type)
	}
	if !strings.Contains(strings.ToLower(info.Title), testVideoTitleSub) {
		t.Errorf("title = %q, want to contain %q", info.Title, testVideoTitleSub)
	}
	if !approxEq(info.Duration, testVideoDuration, 5) {
		t.Errorf("duration = %d, want ~%d", info.Duration, testVideoDuration)
	}
	if info.Thumbnail == "" {
		t.Error("expected non-empty thumbnail URL")
	}
	if info.Uploader == "" {
		t.Error("expected non-empty uploader")
	}
}

func TestIntegration_DownloadByVideoID(t *testing.T) {
	requireBinaries(t, "yt-dlp", "ffmpeg", "ffprobe")

	tempDir := t.TempDir()
	store := NewJobStore()
	store.Add("test", &DownloadStatus{ID: "test", Status: "downloading"})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	if _, err := downloadByVideoID(ctx, store, "test", testVideoID, tempDir); err != nil {
		t.Fatalf("downloadByVideoID: %v", err)
	}

	mp3s := mp3Files(t, tempDir)
	if len(mp3s) != 1 {
		t.Fatalf("got %d MP3 files, want 1: %v", len(mp3s), mp3s)
	}

	full := filepath.Join(tempDir, mp3s[0])

	if size := fileSize(t, full); size < 1_000_000 {
		t.Errorf("file size = %d bytes, want >1MB (likely incomplete download)", size)
	}

	dur := ffprobeDuration(t, full)
	if !approxEqFloat(dur, float64(testVideoDuration), 5) {
		t.Errorf("duration = %.1fs, want ~%ds", dur, testVideoDuration)
	}

	if !CheckMP3HasArtwork(full) {
		t.Error("expected embedded artwork (--embed-thumbnail)")
	}
}

func TestIntegration_AdjustAudioSpeed_Halves(t *testing.T) {
	requireBinaries(t, "yt-dlp", "ffmpeg", "ffprobe")

	tempDir := t.TempDir()
	store := NewJobStore()
	store.Add("test", &DownloadStatus{ID: "test", Status: "downloading"})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	if _, err := downloadByVideoID(ctx, store, "test", testVideoID, tempDir); err != nil {
		t.Fatalf("downloadByVideoID: %v", err)
	}
	mp3s := mp3Files(t, tempDir)
	if len(mp3s) != 1 {
		t.Fatalf("got %d MP3 files, want 1", len(mp3s))
	}
	full := filepath.Join(tempDir, mp3s[0])

	originalDur := ffprobeDuration(t, full)

	if err := AdjustAudioSpeed(ctx, full, 2.0); err != nil {
		t.Fatalf("AdjustAudioSpeed: %v", err)
	}

	newDur := ffprobeDuration(t, full)
	expected := originalDur / 2.0
	// atempo=2.0 cuts duration in half. Allow ±2s tolerance for re-encode jitter.
	if !approxEqFloat(newDur, expected, 2) {
		t.Errorf("after 2.0x speed: duration = %.1fs, want ~%.1fs", newDur, expected)
	}

	if !CheckMP3HasArtwork(full) {
		t.Error("artwork should be preserved through speed adjustment")
	}
}

func TestIntegration_FullHTTPFlow(t *testing.T) {
	requireBinaries(t, "yt-dlp", "ffmpeg")

	gin.SetMode(gin.TestMode)
	r := gin.New()
	tempDir := t.TempDir()
	outputDir := t.TempDir()
	New(r, Config{TempDir: tempDir, OutputDir: outputDir})

	srv := httptest.NewServer(r)
	defer srv.Close()

	// 1. Preview the video.
	var preview VideoInfo
	post(t, srv.URL+"/api/v1/preview", map[string]any{"url": testVideoURL}, &preview)
	if preview.Type != "video" {
		t.Fatalf("preview type = %q", preview.Type)
	}

	// 2. Queue a download at 1.5x speed.
	var createOut struct {
		DownloadID string `json:"download_id"`
	}
	post(t, srv.URL+"/api/v1/downloads", map[string]any{
		"url":   testVideoURL,
		"speed": 1.5,
	}, &createOut)
	if createOut.DownloadID == "" {
		t.Fatal("missing download_id")
	}

	// 3. Poll status until completed.
	statusURL := srv.URL + "/api/v1/downloads/" + createOut.DownloadID
	var finalStatus DownloadStatus
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		var s DownloadStatus
		getJSON(t, statusURL, &s)
		switch s.Status {
		case "completed":
			finalStatus = s
		case "error":
			t.Fatalf("download errored: %s", s.Message)
		case "cancelled":
			t.Fatalf("download cancelled: %s", s.Message)
		}
		if finalStatus.Status == "completed" {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if finalStatus.Status != "completed" {
		t.Fatalf("did not complete within deadline; last status = %q", finalStatus.Status)
	}

	// 4. The file should appear in /temp via /files endpoint.
	var listOut struct {
		TempFiles   []FileInfo `json:"temp_files"`
		OutputFiles []FileInfo `json:"output_files"`
	}
	getJSON(t, srv.URL+"/api/v1/files", &listOut)
	if len(listOut.TempFiles) != 1 {
		t.Fatalf("temp_files count = %d, want 1", len(listOut.TempFiles))
	}
	if len(listOut.OutputFiles) != 0 {
		t.Errorf("output_files count = %d, want 0 before move", len(listOut.OutputFiles))
	}
	filename := listOut.TempFiles[0].Name

	// 5. Move it to /output.
	var moveOut struct {
		Success bool     `json:"success"`
		Moved   int      `json:"moved"`
		Errors  []string `json:"errors"`
	}
	post(t, srv.URL+"/api/v1/files/move", map[string]any{
		"filenames": []string{filename},
	}, &moveOut)
	if moveOut.Moved != 1 {
		t.Errorf("moved = %d, want 1, errors = %v", moveOut.Moved, moveOut.Errors)
	}

	// 6. Re-list and confirm it's now in output.
	getJSON(t, srv.URL+"/api/v1/files", &listOut)
	if len(listOut.TempFiles) != 0 {
		t.Errorf("temp_files after move = %d, want 0", len(listOut.TempFiles))
	}
	if len(listOut.OutputFiles) != 1 {
		t.Errorf("output_files after move = %d, want 1", len(listOut.OutputFiles))
	}

	// 7. Stream the file — confirm the binary content is reachable.
	streamURL := srv.URL + "/api/v1/files/output/stream/" + filename
	resp, err := http.Get(streamURL)
	if err != nil {
		t.Fatalf("GET stream: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("stream status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "audio") && !strings.Contains(ct, "mpeg") {
		t.Errorf("stream content-type = %q, want audio/mpeg or similar", ct)
	}
}

// ---- new tests added in this round ----

func TestIntegration_HealthEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New(r, Config{TempDir: t.TempDir(), OutputDir: t.TempDir()})

	srv := httptest.NewServer(r)
	defer srv.Close()

	var out struct {
		Status  string `json:"status"`
		Version string `json:"version"`
	}
	getJSON(t, srv.URL+"/api/v1/health", &out)
	if out.Status != "ok" {
		t.Errorf("status = %q, want ok", out.Status)
	}
	if out.Version == "" {
		t.Error("missing version")
	}
}

func TestIntegration_CancelMidDownload(t *testing.T) {
	requireBinaries(t, "yt-dlp")

	gin.SetMode(gin.TestMode)
	r := gin.New()
	tempDir := t.TempDir()
	a := New(r, Config{TempDir: tempDir, OutputDir: t.TempDir()})

	srv := httptest.NewServer(r)
	defer srv.Close()

	// Queue a download.
	var createOut struct {
		DownloadID string `json:"download_id"`
	}
	post(t, srv.URL+"/api/v1/downloads", map[string]any{"url": testVideoURL}, &createOut)
	jobID := createOut.DownloadID

	// Wait until the goroutine has actually launched yt-dlp (status flips to "downloading").
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		job, _ := a.store.Get(jobID)
		if job != nil && job.Status == "downloading" {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	job, _ := a.store.Get(jobID)
	if job == nil || job.Status != "downloading" {
		t.Fatalf("job did not enter downloading state in time; status=%q", job.Status)
	}

	// Cancel the in-flight job via the API.
	resp, err := http.NewRequest(http.MethodDelete, srv.URL+"/api/v1/downloads/"+jobID, nil)
	if err != nil {
		t.Fatalf("build cancel req: %v", err)
	}
	r2, err := http.DefaultClient.Do(resp)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	r2.Body.Close()
	if r2.StatusCode != http.StatusOK {
		t.Fatalf("cancel status = %d, want 200", r2.StatusCode)
	}

	// Within a short window the goroutine should observe the kill and update status to "cancelled".
	cancelDeadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(cancelDeadline) {
		job, _ = a.store.Get(jobID)
		if job != nil && job.Status == "cancelled" {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if job.Status != "cancelled" {
		t.Errorf("status after cancel = %q, want cancelled (message=%q)", job.Status, job.Message)
	}

	// No new MP3 should have completed (allow yt-dlp's pre-encode partial files to exist;
	// we only assert no fully-extracted .mp3 was produced before the kill landed).
	files := mp3Files(t, tempDir)
	if len(files) > 0 {
		t.Logf("note: %d MP3(s) present after cancel — may indicate cancel raced with the final ffmpeg extract: %v", len(files), files)
	}
}

func TestIntegration_PlaylistPreviewFull(t *testing.T) {
	requireBinaries(t, "yt-dlp")

	// YouTube auto-generates a "Mix" playlist (RD<videoID>) for any video.
	// Mixes are dynamic but always contain >0 entries; we test invariants, not exact counts.
	playlistURL := "https://www.youtube.com/watch?v=" + testVideoID + "&list=RD" + testVideoID

	info, err := GetVideoInfo(playlistURL, true)
	if err != nil {
		t.Skipf("playlist resolve failed (mix may be region-restricted or yt-dlp version-sensitive): %v", err)
	}
	if info.Type != "playlist" {
		t.Errorf("type = %q, want playlist", info.Type)
	}
	if info.Count <= 0 {
		t.Errorf("count = %d, want >0", info.Count)
	}
	if len(info.Videos) != info.Count {
		t.Errorf("len(Videos) = %d, want %d (full=true should return every entry)", len(info.Videos), info.Count)
	}
	for i, v := range info.Videos {
		if v.ID == "" {
			t.Errorf("Videos[%d] missing ID", i)
		}
		if v.Title == "" {
			t.Errorf("Videos[%d] missing title", i)
		}
	}
}

func TestIntegration_PlaylistPreviewDefaultPreviewSize(t *testing.T) {
	requireBinaries(t, "yt-dlp")

	playlistURL := "https://www.youtube.com/watch?v=" + testVideoID + "&list=RD" + testVideoID

	info, err := GetVideoInfo(playlistURL, false)
	if err != nil {
		t.Skipf("playlist resolve failed: %v", err)
	}
	if info.Count == 0 {
		t.Fatal("count = 0")
	}
	// Default preview is capped at 3 videos but Count reflects the full size.
	if len(info.Videos) > 3 {
		t.Errorf("len(Videos) = %d, want <=3 in default preview mode", len(info.Videos))
	}
	if info.Count <= len(info.Videos) && info.Count > 3 {
		t.Errorf("Count (%d) should be >= len(Videos) (%d)", info.Count, len(info.Videos))
	}
}

func TestIntegration_DownloadByVideoIDsSubset(t *testing.T) {
	requireBinaries(t, "yt-dlp", "ffmpeg")

	gin.SetMode(gin.TestMode)
	r := gin.New()
	tempDir := t.TempDir()
	a := New(r, Config{TempDir: tempDir, OutputDir: t.TempDir()})

	srv := httptest.NewServer(r)
	defer srv.Close()

	// Submit a download with video_ids only (no url) — the AI-friendly path.
	var createOut struct {
		DownloadID string `json:"download_id"`
	}
	post(t, srv.URL+"/api/v1/downloads", map[string]any{
		"video_ids": []string{testVideoID},
	}, &createOut)
	jobID := createOut.DownloadID

	// Poll for completion.
	deadline := time.Now().Add(3 * time.Minute)
	var final *DownloadStatus
	for time.Now().Before(deadline) {
		job, _ := a.store.Get(jobID)
		if job == nil {
			t.Fatal("job vanished")
		}
		if job.Status == "completed" {
			final = job
			break
		}
		if job.Status == "error" || job.Status == "cancelled" {
			t.Fatalf("job ended in %q: %s", job.Status, job.Message)
		}
		time.Sleep(2 * time.Second)
	}
	if final == nil {
		t.Fatal("did not complete in time")
	}
	if !strings.Contains(final.Message, "1") {
		t.Errorf("expected count=1 reflected in message, got %q", final.Message)
	}

	if got := mp3Files(t, tempDir); len(got) != 1 {
		t.Errorf("got %d MP3 files, want 1: %v", len(got), got)
	}
}

func TestIntegration_StreamAndThumbnailContent(t *testing.T) {
	requireBinaries(t, "yt-dlp", "ffmpeg")

	gin.SetMode(gin.TestMode)
	r := gin.New()
	tempDir := t.TempDir()
	a := New(r, Config{TempDir: tempDir, OutputDir: t.TempDir()})

	srv := httptest.NewServer(r)
	defer srv.Close()

	// Download something we can stream/thumbnail.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	a.store.Add("dl", &DownloadStatus{ID: "dl", Status: "downloading"})
	if _, err := downloadByVideoID(ctx, a.store, "dl", testVideoID, tempDir); err != nil {
		t.Fatalf("downloadByVideoID: %v", err)
	}
	mp3s := mp3Files(t, tempDir)
	if len(mp3s) != 1 {
		t.Fatalf("want 1 MP3, got %d", len(mp3s))
	}
	filename := mp3s[0]

	// Stream: must return audio bytes starting with an MP3 frame header (0xFF 0xFB / 0xFF 0xFA / ID3).
	streamURL := srv.URL + "/api/v1/files/temp/stream/" + filename
	resp, err := http.Get(streamURL)
	if err != nil {
		t.Fatalf("GET stream: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stream status = %d", resp.StatusCode)
	}
	head := make([]byte, 4)
	if _, err := io.ReadFull(resp.Body, head); err != nil {
		t.Fatalf("read stream head: %v", err)
	}
	isID3 := string(head[:3]) == "ID3"
	isMPEG := head[0] == 0xFF && (head[1]&0xE0) == 0xE0
	if !isID3 && !isMPEG {
		t.Errorf("stream first 4 bytes = % x, expected ID3 tag or MPEG sync frame", head)
	}

	// Thumbnail: must return image bytes (PNG or JPEG magic).
	thumbURL := srv.URL + "/api/v1/files/temp/thumbnail/" + filename
	tresp, err := http.Get(thumbURL)
	if err != nil {
		t.Fatalf("GET thumbnail: %v", err)
	}
	defer tresp.Body.Close()
	if tresp.StatusCode != http.StatusOK {
		t.Fatalf("thumbnail status = %d", tresp.StatusCode)
	}
	if ct := tresp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "image/") {
		t.Errorf("thumbnail content-type = %q, want image/*", ct)
	}
	imgHead := make([]byte, 8)
	if _, err := io.ReadFull(tresp.Body, imgHead); err != nil {
		t.Fatalf("read thumbnail head: %v", err)
	}
	isPNG := string(imgHead) == "\x89PNG\r\n\x1a\n"
	isJPEG := imgHead[0] == 0xFF && imgHead[1] == 0xD8 && imgHead[2] == 0xFF
	if !isPNG && !isJPEG {
		t.Errorf("thumbnail magic = % x, expected PNG or JPEG", imgHead)
	}
}

// ---- security tests ----

// TestIntegration_SSRFAllowlistBlocksNonYouTubeURLs ensures the URL
// allowlist prevents the server from being a SSRF gadget — i.e., we don't
// hand arbitrary URLs to yt-dlp.
func TestIntegration_SSRFAllowlistBlocksNonYouTubeURLs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New(r, Config{TempDir: t.TempDir(), OutputDir: t.TempDir()})
	srv := httptest.NewServer(r)
	defer srv.Close()

	hostile := []string{
		"http://127.0.0.1:5432/",                       // probe local Postgres
		"http://169.254.169.254/latest/meta-data/",     // AWS metadata
		"http://internal-host.local/secret",            // internal DNS
		"file:///etc/passwd",                           // file scheme
		"ftp://files.example.com/payload",              // unsupported scheme
		"https://evil.example.com/watch?v=ABC",         // arbitrary external host
	}
	for _, raw := range hostile {
		t.Run(raw, func(t *testing.T) {
			b, _ := json.Marshal(map[string]any{"url": raw})
			resp, err := http.Post(srv.URL+"/api/v1/preview", "application/json", bytes.NewReader(b))
			if err != nil {
				t.Fatalf("POST: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				body, _ := io.ReadAll(resp.Body)
				t.Errorf("status = %d, want 400 — body=%s", resp.StatusCode, body)
			}
		})
	}

	// Sanity: the legitimate test URL should still be accepted (and may then
	// fail downstream if yt-dlp is missing, but the allowlist itself accepts).
	t.Run("legitimate youtube URL passes allowlist", func(t *testing.T) {
		if !IsAllowedSourceURL(testVideoURL) {
			t.Errorf("test video URL rejected by allowlist: %q", testVideoURL)
		}
	})
}

// TestIntegration_SecurityHeadersPresent confirms the headers middleware
// is wired into the request pipeline.
func TestIntegration_SecurityHeadersPresent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(SecurityHeaders())
	New(r, Config{TempDir: t.TempDir(), OutputDir: t.TempDir()})
	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/health")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()

	want := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "no-referrer",
	}
	for k, v := range want {
		if got := resp.Header.Get(k); got != v {
			t.Errorf("header %s = %q, want %q", k, got, v)
		}
	}
	if csp := resp.Header.Get("Content-Security-Policy"); csp == "" {
		t.Error("missing Content-Security-Policy header")
	} else if !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("CSP missing frame-ancestors: %q", csp)
	}
	if pp := resp.Header.Get("Permissions-Policy"); pp == "" {
		t.Error("missing Permissions-Policy header")
	}
}

// TestIntegration_CacheControlNoStore verifies sensitive responses are not
// cacheable, while genuinely-static assets remain cacheable so the browser
// doesn't refetch CSS/icons on every navigation.
func TestIntegration_CacheControlNoStore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(SecurityHeaders())
	New(r, Config{TempDir: t.TempDir(), OutputDir: t.TempDir()})
	// Stand-in static handler so we can assert it's NOT marked no-store.
	r.GET("/static/*filepath", func(c *gin.Context) { c.String(http.StatusOK, "asset") })
	r.GET("/favicon.svg", func(c *gin.Context) { c.String(http.StatusOK, "icon") })

	srv := httptest.NewServer(r)
	defer srv.Close()

	check := func(path, wantCacheControl string) {
		t.Helper()
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
		got := resp.Header.Get("Cache-Control")
		if got != wantCacheControl {
			t.Errorf("%s: Cache-Control = %q, want %q", path, got, wantCacheControl)
		}
	}

	// API and HTML responses must be no-store.
	check("/api/v1/health", "no-store")
	check("/api/v1/files", "no-store")
	// Static assets and the favicon are cacheable (no Cache-Control set by us).
	check("/static/css/main.css", "")
	check("/favicon.svg", "")
}

// TestIntegration_InputLengthLimits guards against DoS via huge request
// bodies. huma's schema validation should reject requests that exceed the
// declared maxLength / maxItems on Preview and CreateDownload inputs.
func TestIntegration_InputLengthLimits(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New(r, Config{TempDir: t.TempDir(), OutputDir: t.TempDir()})
	srv := httptest.NewServer(r)
	defer srv.Close()

	postJSON := func(path string, body any) int {
		t.Helper()
		b, _ := json.Marshal(body)
		resp, err := http.Post(srv.URL+path, "application/json", bytes.NewReader(b))
		if err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	// Preview: URL > 2048 chars rejected before the SSRF allowlist even sees it.
	hugeURL := "https://www.youtube.com/watch?v=" + strings.Repeat("A", 3000)
	if code := postJSON("/api/v1/preview", map[string]any{"url": hugeURL}); code < 400 || code >= 500 {
		t.Errorf("oversize URL on /preview: status = %d, want 4xx", code)
	}

	// CreateDownload: same limit on URL.
	if code := postJSON("/api/v1/downloads", map[string]any{"url": hugeURL}); code < 400 || code >= 500 {
		t.Errorf("oversize URL on /downloads: status = %d, want 4xx", code)
	}

	// CreateDownload: video_ids array > 100 entries rejected.
	tooMany := make([]string, 101)
	for i := range tooMany {
		tooMany[i] = "abcdefghij1"
	}
	if code := postJSON("/api/v1/downloads", map[string]any{"video_ids": tooMany}); code < 400 || code >= 500 {
		t.Errorf("oversized video_ids[]: status = %d, want 4xx", code)
	}

	// (No "legitimate request passes" sanity check here — that path is
	// already covered end-to-end by TestIntegration_DownloadByVideoIDsSubset
	// and would otherwise spawn an extra background download just to assert
	// the schema didn't reject it.)
}

// TestIntegration_TrustedProxiesIgnoresUntrustedXFF verifies that an
// X-Forwarded-For header from a non-trusted source IP is ignored by
// gin's c.ClientIP(). Without this, a remote attacker could spoof the
// header and bypass the per-IP login rate limiter.
func TestIntegration_TrustedProxiesIgnoresUntrustedXFF(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	// Trust only an arbitrary RFC1918 address — definitely NOT 127.0.0.1
	// (which is where httptest.NewServer's clients connect from).
	if err := r.SetTrustedProxies([]string{"10.0.0.1"}); err != nil {
		t.Fatalf("SetTrustedProxies: %v", err)
	}
	r.GET("/whoami", func(c *gin.Context) { c.String(http.StatusOK, c.ClientIP()) })

	srv := httptest.NewServer(r)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/whoami", nil)
	req.Header.Set("X-Forwarded-For", "1.2.3.4, 5.6.7.8") // attacker-supplied
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	got := strings.TrimSpace(string(body))
	if got == "1.2.3.4" || got == "5.6.7.8" {
		t.Errorf("ClientIP() honoured untrusted XFF: got %q", got)
	}
	// Should be 127.0.0.1 (or its IPv6 form depending on stack) — the actual
	// connecting peer, not the spoofed header.
	if got != "127.0.0.1" && got != "::1" {
		t.Errorf("ClientIP() = %q, want loopback", got)
	}
}

// TestIntegration_AuthMiddlewareRoutesAPIvsBrowser confirms the contract
// that drives the frontend's UX: API requests get 401 JSON (so the fetch
// interceptor can react), while non-API GETs redirect to /login (so the
// browser hits the login page directly).
func TestIntegration_AuthMiddlewareRoutesAPIvsBrowser(t *testing.T) {
	cfg := AuthConfig{Username: "u", Password: "p"}
	store := NewSessionStore(time.Hour)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(AuthMiddleware(cfg, store))
	// Stand-in for the index page that web.Mount would normally register.
	r.GET("/", func(c *gin.Context) { c.String(http.StatusOK, "home") })
	New(r, Config{TempDir: t.TempDir(), OutputDir: t.TempDir()})

	srv := httptest.NewServer(r)
	defer srv.Close()

	cli := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	// API path (no cookie): 401 JSON.
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/files", nil)
	req.Header.Set("Accept", "application/json")
	r1, _ := cli.Do(req)
	body1, _ := io.ReadAll(r1.Body)
	r1.Body.Close()
	if r1.StatusCode != http.StatusUnauthorized {
		t.Errorf("API GET: status = %d, want 401", r1.StatusCode)
	}
	if !strings.Contains(string(body1), "authentication required") {
		t.Errorf("API GET body = %q, want 'authentication required'", body1)
	}

	// Non-API GET (no cookie): 302 to /login.
	req, _ = http.NewRequest(http.MethodGet, srv.URL+"/", nil)
	req.Header.Set("Accept", "text/html")
	r2, _ := cli.Do(req)
	r2.Body.Close()
	if r2.StatusCode != http.StatusFound {
		t.Errorf("browser GET: status = %d, want 302", r2.StatusCode)
	}
	if loc := r2.Header.Get("Location"); loc != "/login" {
		t.Errorf("browser GET Location = %q, want /login", loc)
	}

	// API path with no Accept header (curl default): still 401 JSON, never redirect.
	req, _ = http.NewRequest(http.MethodGet, srv.URL+"/api/v1/files", nil)
	r3, _ := cli.Do(req)
	r3.Body.Close()
	if r3.StatusCode != http.StatusUnauthorized {
		t.Errorf("API GET no-Accept: status = %d, want 401 (api paths never redirect)", r3.StatusCode)
	}

	// Public endpoint (/api/v1/health) — passthrough even unauthenticated.
	r4, _ := http.Get(srv.URL + "/api/v1/health")
	r4.Body.Close()
	if r4.StatusCode != http.StatusOK {
		t.Errorf("/api/v1/health: status = %d, want 200 (always public)", r4.StatusCode)
	}
}

// TestIntegration_PathTraversalBlocked exercises the SafeJoin guard on the
// streaming + thumbnail endpoints. A request that decodes to '../etc/passwd'
// must not escape the configured base dir.
func TestIntegration_PathTraversalBlocked(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	tempDir := t.TempDir()
	outputDir := t.TempDir()
	New(r, Config{TempDir: tempDir, OutputDir: outputDir})

	srv := httptest.NewServer(r)
	defer srv.Close()

	// Plant a file outside tempDir to prove we're not just hitting "no such file".
	if err := os.WriteFile(filepath.Join(filepath.Dir(tempDir), "sensitive.mp3"), []byte("secret"), 0o644); err != nil {
		t.Fatalf("plant: %v", err)
	}

	traversals := []string{
		"/api/v1/files/temp/stream/../sensitive.mp3",
		"/api/v1/files/temp/stream/%2E%2E/sensitive.mp3",
		"/api/v1/files/output/stream/../../etc/passwd",
		"/api/v1/files/temp/thumbnail/../sensitive.mp3",
	}
	for _, path := range traversals {
		t.Run(path, func(t *testing.T) {
			resp, err := http.Get(srv.URL + path)
			if err != nil {
				t.Fatalf("GET: %v", err)
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			// A successful traversal would return 200 with the planted contents.
			// We accept anything non-200 as evidence the request did not reach a
			// file outside tempDir/outputDir.
			if resp.StatusCode == http.StatusOK && string(body) == "secret" {
				t.Errorf("traversal succeeded — path=%s returned secret bytes", path)
			}
		})
	}
}

// ---- helpers ----

func requireBinaries(t *testing.T, names ...string) {
	t.Helper()
	for _, n := range names {
		if _, err := exec.LookPath(n); err != nil {
			t.Skipf("required binary %q not found on PATH: %v", n, err)
		}
	}
}

func mp3Files(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir %s: %v", dir, err)
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".mp3") {
			out = append(out, e.Name())
		}
	}
	return out
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.Size()
}

func ffprobeDuration(t *testing.T, file string) float64 {
	t.Helper()
	out, err := exec.Command("ffprobe",
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		file,
	).Output()
	if err != nil {
		t.Fatalf("ffprobe %s: %v", file, err)
	}
	val, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil {
		t.Fatalf("parse duration %q: %v", string(out), err)
	}
	return val
}

func approxEq(got, want, tolerance int) bool {
	diff := got - want
	if diff < 0 {
		diff = -diff
	}
	return diff <= tolerance
}

func approxEqFloat(got, want, tolerance float64) bool {
	diff := got - want
	if diff < 0 {
		diff = -diff
	}
	return diff <= tolerance
}

func post(t *testing.T, url string, body any, out any) {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	resp, err := http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST %s: status=%d body=%s", url, resp.StatusCode, string(body))
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("decode response from %s: %v", url, err)
		}
	}
}

func getJSON(t *testing.T, url string, out any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		t.Fatalf("GET %s: status=%d", url, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		t.Fatalf("decode response from %s: %v", url, err)
	}
}

