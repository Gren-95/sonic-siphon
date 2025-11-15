package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type FileInfo struct {
	Name         string  `json:"name"`
	Size         float64 `json:"size"`
	Modified     int64   `json:"modified"`
	HasThumbnail bool    `json:"has_thumbnail"`
	Location     string  `json:"location"`
}

func getFilesInDir(dir, location string) ([]FileInfo, error) {
	files := []FileInfo{}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return files, nil
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		filename := entry.Name()
		if !strings.HasSuffix(strings.ToLower(filename), ".mp3") {
			continue
		}

		filepath := filepath.Join(dir, filename)
		info, err := entry.Info()
		if err != nil {
			continue
		}

		fileSizeMB := float64(info.Size()) / (1024 * 1024) // Convert to MB
		// Round to 2 decimal places
		fileSize := float64(int(fileSizeMB*100+0.5)) / 100
		hasThumbnail := checkMP3HasArtwork(filepath)

		files = append(files, FileInfo{
			Name:         filename,
			Size:         fileSize,
			Modified:     info.ModTime().Unix(),
			HasThumbnail: hasThumbnail,
			Location:     location,
		})
	}

	// Sort by modification time, newest first
	sort.Slice(files, func(i, j int) bool {
		return files[i].Modified > files[j].Modified
	})

	return files, nil
}

func checkMP3HasArtwork(filepath string) bool {
	cmd := exec.Command("ffprobe", "-v", "quiet", "-select_streams", "v:0",
		"-show_entries", "stream=codec_name", "-of", "default=noprint_wrappers=1:nokey=1", filepath)
	output, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(output)) != ""
}


