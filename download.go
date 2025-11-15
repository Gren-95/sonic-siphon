package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type VideoInfo struct {
	Type      string      `json:"type"`
	Title     string      `json:"title"`
	Duration  int         `json:"duration"`
	Thumbnail string      `json:"thumbnail"`
	Uploader  string      `json:"uploader,omitempty"`
	Count     int         `json:"count,omitempty"`
	Videos    []VideoItem `json:"videos,omitempty"`
}

type VideoItem struct {
	Title     string `json:"title"`
	Duration  int    `json:"duration"`
	Thumbnail string `json:"thumbnail"`
}

// getPlaylistVideoIDs extracts all video IDs from a playlist using flat extraction
func getPlaylistVideoIDs(url string) ([]string, string, error) {
	// CRITICAL: Must use --yes-playlist for URLs with both watch?v= and list= parameters
	ydlOpts := []string{
		"--yes-playlist",      // Force playlist mode
		"--flat-playlist",
		"--print-json",
		"--no-warnings",
		"--skip-download",
		"--extractor-args", "youtube:player_client=android,web",
		url,
	}

	fmt.Printf("[DEBUG] Getting playlist info with: yt-dlp %s\n", strings.Join(ydlOpts, " "))

	cmd := exec.Command("yt-dlp", ydlOpts...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Printf("[ERROR] yt-dlp failed: %v\nOutput: %s\n", err, string(output))
		return nil, "", fmt.Errorf("failed to get playlist info: %v, output: %s", err, string(output))
	}

	outputStr := strings.TrimSpace(string(output))
	if outputStr == "" {
		return nil, "", fmt.Errorf("yt-dlp returned empty output")
	}

	// Parse line-by-line JSON (each line is a separate video entry)
	lines := strings.Split(outputStr, "\n")
	var videoIDs []string
	var playlistTitle string

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var entry map[string]interface{}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			fmt.Printf("[WARN] Failed to parse JSON line: %v\n", err)
			continue
		}

		// Get video ID
		if id, ok := entry["id"].(string); ok && id != "" {
			videoIDs = append(videoIDs, id)
		}

		// Try to get playlist title from the first entry
		if playlistTitle == "" {
			if title, ok := entry["playlist_title"].(string); ok && title != "" {
				playlistTitle = title
			} else if title, ok := entry["playlist"].(string); ok && title != "" {
				playlistTitle = title
			}
		}
	}

	if len(videoIDs) == 0 {
		// Fallback: try using --get-id instead (with --yes-playlist!)
		fmt.Printf("[INFO] No videos found with --print-json, trying --get-id\n")
		
		ydlOpts = []string{
			"--yes-playlist",  // Still need this!
			"--flat-playlist",
			"--get-id",
			"--no-warnings",
			"--extractor-args", "youtube:player_client=android,web",
			url,
		}

		cmd = exec.Command("yt-dlp", ydlOpts...)
		output, err = cmd.CombinedOutput()
		if err != nil {
			return nil, "", fmt.Errorf("failed to get playlist IDs: %v, output: %s", err, string(output))
		}

		outputStr = strings.TrimSpace(string(output))
		if outputStr == "" {
			return nil, "", fmt.Errorf("no videos found in playlist")
		}

		// Each line is a video ID
		lines = strings.Split(outputStr, "\n")
		for _, id := range lines {
			id = strings.TrimSpace(id)
			if id != "" {
				videoIDs = append(videoIDs, id)
			}
		}
	}

	if len(videoIDs) == 0 {
		return nil, "", fmt.Errorf("no videos found in playlist")
	}

	fmt.Printf("[INFO] Found %d videos in playlist\n", len(videoIDs))

	return videoIDs, playlistTitle, nil
}

func getVideoInfo(url string) (*VideoInfo, error) {
	// Check if URL contains playlist parameter
	isPlaylist := strings.Contains(url, "list=") || 
		strings.Contains(url, "/playlist") || 
		strings.Contains(url, "playlist?list=") ||
		strings.HasPrefix(url, "https://www.youtube.com/playlist") ||
		strings.HasPrefix(url, "http://www.youtube.com/playlist") ||
		strings.HasPrefix(url, "https://youtube.com/playlist") ||
		strings.HasPrefix(url, "http://youtube.com/playlist")

	// For playlists, use flat extraction to get all video IDs
	if isPlaylist {
		videoIDs, playlistTitle, err := getPlaylistVideoIDs(url)
		if err != nil {
			return nil, err
		}

		// Get info for the first few videos to show preview
		videos := []VideoItem{}
		maxVideos := 3
		if len(videoIDs) < maxVideos {
			maxVideos = len(videoIDs)
		}

		for i := 0; i < maxVideos; i++ {
			videoURL := "https://www.youtube.com/watch?v=" + videoIDs[i]
			ydlOpts := []string{
				"--quiet",
				"--no-warnings",
				"--dump-json",
				"--no-playlist",
				"--extractor-args", "youtube:player_client=android,web",
				videoURL,
			}

			cmd := exec.Command("yt-dlp", ydlOpts...)
			output, err := cmd.CombinedOutput()
			if err == nil {
				var videoInfo map[string]interface{}
				if json.Unmarshal(output, &videoInfo) == nil {
					video := VideoItem{
						Title:     getString(videoInfo, "title"),
						Thumbnail: getString(videoInfo, "thumbnail"),
					}
					if duration, ok := videoInfo["duration"].(float64); ok {
						video.Duration = int(duration)
					}
					videos = append(videos, video)
				}
			}
		}

		thumbnail := ""
		if len(videos) > 0 {
			thumbnail = videos[0].Thumbnail
		}

		title := playlistTitle
		if title == "" {
			title = fmt.Sprintf("Playlist (%d videos)", len(videoIDs))
		}

		return &VideoInfo{
			Type:      "playlist",
			Title:     title,
			Count:     len(videoIDs),
			Videos:    videos,
			Thumbnail: thumbnail,
		}, nil
	}

	// Single video - use --no-playlist to ensure we only get the video
	ydlOpts := []string{
		"--quiet",
		"--no-warnings",
		"--dump-json",
		"--no-playlist",
		"--extractor-args", "youtube:player_client=android,web",
		url,
	}

	cmd := exec.Command("yt-dlp", ydlOpts...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("failed to get video info: %v, output: %s", err, string(output))
	}

	outputStr := strings.TrimSpace(string(output))
	if outputStr == "" {
		return nil, fmt.Errorf("yt-dlp returned empty output")
	}

	var info map[string]interface{}
	if err := json.Unmarshal([]byte(outputStr), &info); err != nil {
		return nil, fmt.Errorf("failed to parse video info: %v", err)
	}

	// Single video
	duration := 0
	if d, ok := info["duration"].(float64); ok {
		duration = int(d)
	}

	return &VideoInfo{
		Type:      "video",
		Title:     getString(info, "title"),
		Duration:  duration,
		Thumbnail: getString(info, "thumbnail"),
		Uploader:  getString(info, "uploader"),
	}, nil
}

func getString(m map[string]interface{}, key string) string {
	if val, ok := m[key].(string); ok {
		return val
	}
	return ""
}

// downloadSingleVideo downloads a single video with yt-dlp
func downloadSingleVideo(ctx context.Context, videoID string, tempDir string) (string, error) {
	videoURL := "https://www.youtube.com/watch?v=" + videoID
	
	ydlOpts := []string{
		"-f", "bestaudio/best",
		"-o", filepath.Join(tempDir, "%(title)s.%(ext)s"),
		"--write-thumbnail",
		"--extract-audio",
		"--audio-format", "mp3",
		"--audio-quality", "192K",
		"--embed-thumbnail",
		"--add-metadata",
		"--no-playlist",
		"--extractor-args", "youtube:player_client=android,web",
		"--no-check-certificate",
		"--print", "after_move:filepath",
		videoURL,
	}

	cmd := exec.CommandContext(ctx, "yt-dlp", ydlOpts...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("download failed: %v, output: %s", err, string(output))
	}

	// Extract the file path from output
	outputStr := strings.TrimSpace(string(output))
	lines := strings.Split(outputStr, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasSuffix(line, ".mp3") && strings.HasPrefix(line, tempDir) {
			return line, nil
		}
	}

	// Fallback: find the most recently created MP3 file
	files := getMP3Files(tempDir)
	if len(files) == 0 {
		return "", fmt.Errorf("no MP3 file created")
	}

	// Return the most recently modified file
	var newestFile string
	var newestTime time.Time
	for f := range files {
		fullPath := filepath.Join(tempDir, f)
		info, err := os.Stat(fullPath)
		if err == nil && info.ModTime().After(newestTime) {
			newestTime = info.ModTime()
			newestFile = fullPath
		}
	}

	if newestFile == "" {
		return "", fmt.Errorf("could not find downloaded file")
	}

	return newestFile, nil
}

func downloadTask(ctx context.Context, url string, speed float64, downloadID string) {
	// Check if cancelled before starting
	select {
	case <-ctx.Done():
		mu.Lock()
		if job, exists := downloads[downloadID]; exists {
			job.Status = "cancelled"
			job.Message = "Job cancelled before starting"
		}
		mu.Unlock()
		return
	default:
	}

	mu.Lock()
	downloads[downloadID].Status = "downloading"
	downloads[downloadID].Message = "Starting download..."
	mu.Unlock()

	// Check if URL contains playlist parameter
	isPlaylist := strings.Contains(url, "list=") || 
		strings.Contains(url, "/playlist") || 
		strings.Contains(url, "playlist?list=") ||
		strings.HasPrefix(url, "https://www.youtube.com/playlist") ||
		strings.HasPrefix(url, "http://www.youtube.com/playlist") ||
		strings.HasPrefix(url, "https://youtube.com/playlist") ||
		strings.HasPrefix(url, "http://youtube.com/playlist")

	// Handle playlists - let yt-dlp download all at once
	if isPlaylist {
		fmt.Printf("[INFO] Detected playlist, downloading with yt-dlp\n")
		
		mu.Lock()
		downloads[downloadID].Message = "Downloading playlist..."
		mu.Unlock()

		// Build yt-dlp command for playlist
		ydlOpts := []string{
			"--yes-playlist",  // Force playlist mode
			"-f", "bestaudio/best",
			"-o", filepath.Join(TEMP_DIR, "%(title)s.%(ext)s"),
			"--write-thumbnail",
			"--extract-audio",
			"--audio-format", "mp3",
			"--audio-quality", "192K",
			"--embed-thumbnail",
			"--add-metadata",
			"--ignore-errors",  // Continue on errors
			"--no-playlist-reverse",
			"--extractor-args", "youtube:player_client=android,web",
			"--no-check-certificate",
			url,
		}

		fmt.Printf("[DEBUG] Running: yt-dlp %s\n", strings.Join(ydlOpts, " "))

		cmd := exec.CommandContext(ctx, "yt-dlp", ydlOpts...)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = io.MultiWriter(os.Stdout, &stdout)
		cmd.Stderr = io.MultiWriter(os.Stderr, &stderr)

		// Store command reference for cancellation
		mu.Lock()
		downloads[downloadID].cmd = cmd
		mu.Unlock()

		// Get list of files before
		filesBefore := getMP3Files(TEMP_DIR)

		// Run the download
		err := cmd.Run()
		if err != nil {
			mu.Lock()
			if job, exists := downloads[downloadID]; exists {
				if ctx.Err() == context.Canceled {
					job.Status = "cancelled"
					job.Message = "Download cancelled"
				} else {
					errMsg := fmt.Sprintf("Playlist download error: %v", err)
					if stderr.Len() > 0 {
						errMsg += "\n" + stderr.String()[:min(500, stderr.Len())]
					}
					job.Status = "error"
					job.Message = errMsg
				}
			}
			mu.Unlock()
			return
		}

		// Get new files
		filesAfter := getMP3Files(TEMP_DIR)
		var newFiles []string
		for f := range filesAfter {
			if !filesBefore[f] {
				newFiles = append(newFiles, f)
			}
		}

		fmt.Printf("[INFO] Downloaded %d files from playlist\n", len(newFiles))

		// Apply speed adjustment to all files if needed
		if speed != 1.0 && len(newFiles) > 0 {
			mu.Lock()
			downloads[downloadID].Status = "processing"
			downloads[downloadID].Message = fmt.Sprintf("Applying %.1fx speed to %d files...", speed, len(newFiles))
			mu.Unlock()

			successCount := 0
			for i, filename := range newFiles {
				select {
				case <-ctx.Done():
					mu.Lock()
					if job, exists := downloads[downloadID]; exists {
						job.Status = "cancelled"
						job.Message = fmt.Sprintf("Cancelled after processing %d/%d files", i, len(newFiles))
					}
					mu.Unlock()
					return
				default:
				}

				mu.Lock()
				downloads[downloadID].Message = fmt.Sprintf("Processing %d/%d: %s", i+1, len(newFiles), filename)
				mu.Unlock()

				filePath := filepath.Join(TEMP_DIR, filename)
				err := adjustAudioSpeed(ctx, filePath, speed)
				if err != nil {
					fmt.Printf("[WARN] Failed to adjust speed for %s: %v\n", filename, err)
				} else {
					successCount++
				}
			}

			fmt.Printf("[INFO] Applied speed adjustment to %d/%d files\n", successCount, len(newFiles))
		}

		// Mark as completed
		mu.Lock()
		if job, exists := downloads[downloadID]; exists {
			job.Status = "completed"
			if speed != 1.0 {
				job.Message = fmt.Sprintf("Downloaded %d files with %.1fx speed", len(newFiles), speed)
			} else {
				job.Message = fmt.Sprintf("Downloaded %d files", len(newFiles))
			}
		}
		mu.Unlock()

		fmt.Printf("[INFO] Playlist download completed\n")
		return
	}

	// Single video download
	fmt.Printf("[INFO] Downloading single video\n")
	
	mu.Lock()
	downloads[downloadID].Message = "Downloading video..."
	mu.Unlock()

	filePath, err := downloadSingleVideo(ctx, "", TEMP_DIR) // Empty videoID means use full URL
	if err != nil {
		// For single videos, retry with the full URL
		videoURL := url
		ydlOpts := []string{
			"-f", "bestaudio/best",
			"-o", filepath.Join(TEMP_DIR, "%(title)s.%(ext)s"),
			"--write-thumbnail",
			"--extract-audio",
			"--audio-format", "mp3",
			"--audio-quality", "192K",
			"--embed-thumbnail",
			"--add-metadata",
			"--no-playlist",
			"--extractor-args", "youtube:player_client=android,web",
			"--no-check-certificate",
			videoURL,
		}

		cmd := exec.CommandContext(ctx, "yt-dlp", ydlOpts...)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = io.MultiWriter(os.Stdout, &stdout)
		cmd.Stderr = io.MultiWriter(os.Stderr, &stderr)

		// Store command reference for cancellation
		mu.Lock()
		downloads[downloadID].cmd = cmd
		mu.Unlock()

		err = cmd.Run()
		if err != nil {
			mu.Lock()
			if job, exists := downloads[downloadID]; exists {
				if ctx.Err() == context.Canceled {
					job.Status = "cancelled"
					job.Message = "Download cancelled"
				} else {
					errMsg := fmt.Sprintf("Error: %v", err)
					if stderr.Len() > 0 {
						errMsg += "\n" + strings.TrimSpace(stderr.String())
					}
					job.Status = "error"
					job.Message = errMsg
				}
			}
			mu.Unlock()
			return
		}

		// Find the downloaded file
		files := getMP3Files(TEMP_DIR)
		if len(files) == 0 {
			mu.Lock()
			if job, exists := downloads[downloadID]; exists {
				job.Status = "error"
				job.Message = "No MP3 file was created"
			}
			mu.Unlock()
			return
		}

		// Get the most recent file
		var newestFile string
		var newestTime time.Time
		for f := range files {
			fullPath := filepath.Join(TEMP_DIR, f)
			info, err := os.Stat(fullPath)
			if err == nil && info.ModTime().After(newestTime) {
				newestTime = info.ModTime()
				newestFile = fullPath
			}
		}

		filePath = newestFile
	}

	fmt.Printf("[INFO] Downloaded file: %s\n", filePath)

	// Apply speed adjustment if needed
	if speed != 1.0 {
		mu.Lock()
		downloads[downloadID].Status = "processing"
		downloads[downloadID].Message = fmt.Sprintf("Applying %.1fx speed adjustment...", speed)
		mu.Unlock()

		err = adjustAudioSpeed(ctx, filePath, speed)
		if err != nil {
			mu.Lock()
			if job, exists := downloads[downloadID]; exists {
				job.Status = "error"
				job.Message = fmt.Sprintf("Failed to adjust speed: %v", err)
			}
			mu.Unlock()
			return
		}

		fmt.Printf("[INFO] Applied %.1fx speed to file\n", speed)
	}

	// Mark as completed
	mu.Lock()
	if job, exists := downloads[downloadID]; exists {
		job.Status = "completed"
		if speed != 1.0 {
			job.Message = fmt.Sprintf("Download completed with %.1fx speed", speed)
		} else {
			job.Message = "Download completed"
		}
	}
	mu.Unlock()

	fmt.Printf("[INFO] Single video download completed\n")
}

// adjustAudioSpeed adjusts the speed of an audio file using ffmpeg
func adjustAudioSpeed(ctx context.Context, inputFile string, speed float64) error {
	tempFile := inputFile + ".tmp.mp3"
	
	// Use ffmpeg atempo filter (supports 0.5x to 2.0x)
	// For speeds outside this range, chain multiple atempo filters
	var filterChain string
	if speed >= 0.5 && speed <= 2.0 {
		filterChain = fmt.Sprintf("atempo=%.4f", speed)
	} else if speed > 2.0 {
		// Chain multiple atempo filters for speeds > 2.0
		remaining := speed
		filters := []string{}
		for remaining > 2.0 {
			filters = append(filters, "atempo=2.0")
			remaining /= 2.0
		}
		if remaining > 1.0 {
			filters = append(filters, fmt.Sprintf("atempo=%.4f", remaining))
		}
		filterChain = strings.Join(filters, ",")
	} else {
		// Chain multiple atempo filters for speeds < 0.5
		remaining := speed
		filters := []string{}
		for remaining < 0.5 {
			filters = append(filters, "atempo=0.5")
			remaining /= 0.5
		}
		if remaining < 1.0 {
			filters = append(filters, fmt.Sprintf("atempo=%.4f", remaining))
		}
		filterChain = strings.Join(filters, ",")
	}

	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-i", inputFile,
		"-filter:a", filterChain,
		"-map", "0:a",
		"-map", "0:v?",
		"-map_metadata", "0",  // Preserve all metadata from input
		"-c:v", "copy",
		"-id3v2_version", "3",
		"-metadata:s:v", "title=Album cover",
		"-metadata:s:v", "comment=Cover (front)",
		"-acodec", "libmp3lame",
		"-b:a", "192k",
		"-y",
		tempFile,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg failed: %v, output: %s", err, string(output))
	}

	// Replace original file with processed file
	if err := os.Remove(inputFile); err != nil {
		return fmt.Errorf("failed to remove original file: %v", err)
	}

	if err := os.Rename(tempFile, inputFile); err != nil {
		return fmt.Errorf("failed to rename temp file: %v", err)
	}

	return nil
}

// getMP3Files returns a map of MP3 files in the given directory (for old code compatibility)
func getMP3Files(dir string) map[string]bool {
	files := make(map[string]bool)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return files
	}

	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(strings.ToLower(entry.Name()), ".mp3") {
			files[entry.Name()] = true
		}
	}
	return files
}

// changeAudioSpeed is kept for backwards compatibility (unused in new batch download)
func changeAudioSpeed(inputFile, outputFile string, speed float64) bool {
	_ = inputFile
	_ = outputFile
	_ = speed
	return false
}
