package api

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type VideoInfo struct {
	Type      string      `json:"type" doc:"'video' or 'playlist'"`
	Title     string      `json:"title"`
	Duration  int         `json:"duration" doc:"Duration in seconds (single videos only)"`
	Thumbnail string      `json:"thumbnail"`
	Uploader  string      `json:"uploader,omitempty"`
	Count     int         `json:"count,omitempty" doc:"Number of videos in playlist"`
	Videos    []VideoItem `json:"videos,omitempty" doc:"Per-video metadata for playlists"`
}

type VideoItem struct {
	ID        string `json:"id,omitempty" doc:"YouTube video ID"`
	Title     string `json:"title"`
	Duration  int    `json:"duration"`
	Thumbnail string `json:"thumbnail"`
}

func isPlaylistURL(url string) bool {
	return strings.Contains(url, "list=") ||
		strings.Contains(url, "/playlist") ||
		strings.HasPrefix(url, "https://www.youtube.com/playlist") ||
		strings.HasPrefix(url, "http://www.youtube.com/playlist") ||
		strings.HasPrefix(url, "https://youtube.com/playlist") ||
		strings.HasPrefix(url, "http://youtube.com/playlist")
}

func getPlaylistVideoIDs(url string) ([]VideoItem, string, error) {
	ydlOpts := []string{
		"--yes-playlist",
		"--flat-playlist",
		"--print-json",
		"--no-warnings",
		"--skip-download",
		"--extractor-args", "youtube:player_client=android,web",
		url,
	}

	cmd := exec.Command("yt-dlp", ydlOpts...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, "", fmt.Errorf("failed to get playlist info: %v, output: %s", err, string(output))
	}

	outputStr := strings.TrimSpace(string(output))
	if outputStr == "" {
		return nil, "", fmt.Errorf("yt-dlp returned empty output")
	}

	var items []VideoItem
	var playlistTitle string

	for _, line := range strings.Split(outputStr, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry map[string]interface{}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		item := VideoItem{
			ID:    getString(entry, "id"),
			Title: getString(entry, "title"),
		}
		if d, ok := entry["duration"].(float64); ok {
			item.Duration = int(d)
		}
		if t := getString(entry, "thumbnail"); t != "" {
			item.Thumbnail = t
		}
		if item.ID != "" {
			items = append(items, item)
		}
		if playlistTitle == "" {
			if t := getString(entry, "playlist_title"); t != "" {
				playlistTitle = t
			} else if t := getString(entry, "playlist"); t != "" {
				playlistTitle = t
			}
		}
	}

	if len(items) == 0 {
		return nil, "", fmt.Errorf("no videos found in playlist")
	}

	return items, playlistTitle, nil
}

func GetVideoInfo(url string, fullPlaylist bool) (*VideoInfo, error) {
	if isPlaylistURL(url) {
		items, playlistTitle, err := getPlaylistVideoIDs(url)
		if err != nil {
			return nil, err
		}

		var preview []VideoItem
		if fullPlaylist {
			preview = items
		} else {
			max := 3
			if len(items) < max {
				max = len(items)
			}
			preview = make([]VideoItem, 0, max)
			for i := 0; i < max; i++ {
				videoURL := "https://www.youtube.com/watch?v=" + items[i].ID
				detail, err := dumpSingleVideoJSON(videoURL)
				if err == nil {
					vi := VideoItem{
						ID:        items[i].ID,
						Title:     getString(detail, "title"),
						Thumbnail: getString(detail, "thumbnail"),
					}
					if d, ok := detail["duration"].(float64); ok {
						vi.Duration = int(d)
					}
					preview = append(preview, vi)
				} else {
					preview = append(preview, items[i])
				}
			}
		}

		thumbnail := ""
		if len(preview) > 0 {
			thumbnail = preview[0].Thumbnail
		}

		title := playlistTitle
		if title == "" {
			title = fmt.Sprintf("Playlist (%d videos)", len(items))
		}

		return &VideoInfo{
			Type:      "playlist",
			Title:     title,
			Count:     len(items),
			Videos:    preview,
			Thumbnail: thumbnail,
		}, nil
	}

	info, err := dumpSingleVideoJSON(url)
	if err != nil {
		return nil, err
	}

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

func dumpSingleVideoJSON(url string) (map[string]interface{}, error) {
	cmd := exec.Command("yt-dlp",
		"--quiet",
		"--no-warnings",
		"--dump-json",
		"--no-playlist",
		"--extractor-args", "youtube:player_client=android,web",
		url,
	)
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
	return info, nil
}

func getString(m map[string]interface{}, key string) string {
	if val, ok := m[key].(string); ok {
		return val
	}
	return ""
}

// runDownloadTask is the goroutine that downloads + speed-adjusts an audio source.
// videoIDs (optional) restricts a playlist download to specific YouTube IDs.
func runDownloadTask(ctx context.Context, store *JobStore, downloadID, url string, speed float64, tempDir string, videoIDs []string) {
	select {
	case <-ctx.Done():
		store.Update(downloadID, func(j *DownloadStatus) {
			j.Status = "cancelled"
			j.Message = "Job cancelled before starting"
		})
		return
	default:
	}

	store.Update(downloadID, func(j *DownloadStatus) {
		j.Status = "downloading"
		j.Message = "Starting download..."
	})

	if isPlaylistURL(url) && len(videoIDs) == 0 {
		runPlaylistDownload(ctx, store, downloadID, url, speed, tempDir)
		return
	}

	if len(videoIDs) > 0 {
		runVideoIDsDownload(ctx, store, downloadID, videoIDs, speed, tempDir)
		return
	}

	runSingleDownload(ctx, store, downloadID, url, speed, tempDir)
}

func runPlaylistDownload(ctx context.Context, store *JobStore, downloadID, url string, speed float64, tempDir string) {
	store.Update(downloadID, func(j *DownloadStatus) { j.Message = "Downloading playlist..." })

	ydlOpts := []string{
		"--yes-playlist",
		"-f", "bestaudio/best",
		"-o", filepath.Join(tempDir, "%(title)s.%(ext)s"),
		"--write-thumbnail",
		"--extract-audio",
		"--audio-format", "mp3",
		"--audio-quality", "192K",
		"--embed-thumbnail",
		"--add-metadata",
		"--ignore-errors",
		"--no-playlist-reverse",
		"--extractor-args", "youtube:player_client=android,web",
		"--no-check-certificate",
		url,
	}

	cmd := exec.CommandContext(ctx, "yt-dlp", ydlOpts...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	store.attachCmd(downloadID, cmd)

	filesBefore := getMP3Files(tempDir)
	err := cmd.Run()
	if err != nil {
		store.Update(downloadID, func(j *DownloadStatus) {
			if ctx.Err() == context.Canceled {
				j.Status = "cancelled"
				j.Message = "Download cancelled"
			} else {
				j.Status = "error"
				j.Message = fmt.Sprintf("Playlist download error: %v", err)
			}
		})
		return
	}

	var newFiles []string
	for f := range getMP3Files(tempDir) {
		if !filesBefore[f] {
			newFiles = append(newFiles, f)
		}
	}

	applySpeedToFiles(ctx, store, downloadID, tempDir, newFiles, speed)
	finalize(store, downloadID, len(newFiles), speed)
}

func runVideoIDsDownload(ctx context.Context, store *JobStore, downloadID string, videoIDs []string, speed float64, tempDir string) {
	store.Update(downloadID, func(j *DownloadStatus) {
		j.Message = fmt.Sprintf("Downloading %d videos...", len(videoIDs))
	})

	filesBefore := getMP3Files(tempDir)

	for i, id := range videoIDs {
		select {
		case <-ctx.Done():
			store.Update(downloadID, func(j *DownloadStatus) {
				j.Status = "cancelled"
				j.Message = fmt.Sprintf("Cancelled after %d/%d videos", i, len(videoIDs))
			})
			return
		default:
		}

		store.Update(downloadID, func(j *DownloadStatus) {
			j.Message = fmt.Sprintf("Downloading %d/%d", i+1, len(videoIDs))
			j.Progress = fmt.Sprintf("%d%%", (i*100)/max1(len(videoIDs)))
		})

		if _, err := downloadByVideoID(ctx, store, downloadID, id, tempDir); err != nil {
			fmt.Printf("[WARN] download of %s failed: %v\n", id, err)
		}
	}

	var newFiles []string
	for f := range getMP3Files(tempDir) {
		if !filesBefore[f] {
			newFiles = append(newFiles, f)
		}
	}

	applySpeedToFiles(ctx, store, downloadID, tempDir, newFiles, speed)
	finalize(store, downloadID, len(newFiles), speed)
}

func runSingleDownload(ctx context.Context, store *JobStore, downloadID, url string, speed float64, tempDir string) {
	store.Update(downloadID, func(j *DownloadStatus) { j.Message = "Downloading video..." })

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
		url,
	}

	cmd := exec.CommandContext(ctx, "yt-dlp", ydlOpts...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	store.attachCmd(downloadID, cmd)

	filesBefore := getMP3Files(tempDir)
	if err := cmd.Run(); err != nil {
		store.Update(downloadID, func(j *DownloadStatus) {
			if ctx.Err() == context.Canceled {
				j.Status = "cancelled"
				j.Message = "Download cancelled"
			} else {
				j.Status = "error"
				j.Message = fmt.Sprintf("Error: %v", err)
			}
		})
		return
	}

	var newFile string
	var newest time.Time
	for f := range getMP3Files(tempDir) {
		if filesBefore[f] {
			continue
		}
		full := filepath.Join(tempDir, f)
		if info, err := os.Stat(full); err == nil && info.ModTime().After(newest) {
			newest = info.ModTime()
			newFile = full
		}
	}

	if newFile == "" {
		store.Update(downloadID, func(j *DownloadStatus) {
			j.Status = "error"
			j.Message = "No MP3 file was created"
		})
		return
	}

	if speed != 1.0 {
		store.Update(downloadID, func(j *DownloadStatus) {
			j.Status = "processing"
			j.Message = fmt.Sprintf("Applying %.1fx speed adjustment...", speed)
		})
		if err := AdjustAudioSpeed(ctx, newFile, speed); err != nil {
			store.Update(downloadID, func(j *DownloadStatus) {
				j.Status = "error"
				j.Message = fmt.Sprintf("Failed to adjust speed: %v", err)
			})
			return
		}
	}

	finalize(store, downloadID, 1, speed)
}

func downloadByVideoID(ctx context.Context, store *JobStore, downloadID, videoID, tempDir string) (string, error) {
	videoURL := "https://www.youtube.com/watch?v=" + videoID
	cmd := exec.CommandContext(ctx, "yt-dlp",
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
		videoURL,
	)
	store.attachCmd(downloadID, cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("download failed: %v, output: %s", err, string(output))
	}
	return videoID, nil
}

func applySpeedToFiles(ctx context.Context, store *JobStore, downloadID, tempDir string, files []string, speed float64) {
	if speed == 1.0 || len(files) == 0 {
		return
	}
	store.Update(downloadID, func(j *DownloadStatus) {
		j.Status = "processing"
		j.Message = fmt.Sprintf("Applying %.1fx speed to %d files...", speed, len(files))
	})

	for i, filename := range files {
		select {
		case <-ctx.Done():
			store.Update(downloadID, func(j *DownloadStatus) {
				j.Status = "cancelled"
				j.Message = fmt.Sprintf("Cancelled after processing %d/%d files", i, len(files))
			})
			return
		default:
		}
		store.Update(downloadID, func(j *DownloadStatus) {
			j.Message = fmt.Sprintf("Processing %d/%d: %s", i+1, len(files), filename)
		})
		if err := AdjustAudioSpeed(ctx, filepath.Join(tempDir, filename), speed); err != nil {
			fmt.Printf("[WARN] Failed to adjust speed for %s: %v\n", filename, err)
		}
	}
}

func finalize(store *JobStore, downloadID string, count int, speed float64) {
	store.Update(downloadID, func(j *DownloadStatus) {
		j.Status = "completed"
		j.Progress = "100%"
		if speed != 1.0 {
			j.Message = fmt.Sprintf("Downloaded %d files with %.1fx speed", count, speed)
		} else {
			j.Message = fmt.Sprintf("Downloaded %d files", count)
		}
	})
}

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

func max1(n int) int {
	if n == 0 {
		return 1
	}
	return n
}
