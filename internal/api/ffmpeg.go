package api

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// buildAtempoFilterChain returns the ffmpeg filter string that achieves the requested speed.
// ffmpeg's atempo filter only accepts 0.5-2.0; outside that range we chain multiple filters.
func buildAtempoFilterChain(speed float64) string {
	switch {
	case speed >= 0.5 && speed <= 2.0:
		return fmt.Sprintf("atempo=%.4f", speed)
	case speed > 2.0:
		remaining := speed
		filters := []string{}
		for remaining > 2.0 {
			filters = append(filters, "atempo=2.0")
			remaining /= 2.0
		}
		if remaining > 1.0 {
			filters = append(filters, fmt.Sprintf("atempo=%.4f", remaining))
		}
		return strings.Join(filters, ",")
	default:
		remaining := speed
		filters := []string{}
		for remaining < 0.5 {
			filters = append(filters, "atempo=0.5")
			remaining /= 0.5
		}
		if remaining < 1.0 {
			filters = append(filters, fmt.Sprintf("atempo=%.4f", remaining))
		}
		return strings.Join(filters, ",")
	}
}

// AdjustAudioSpeed re-encodes the file in-place at the requested speed using ffmpeg's atempo filter.
func AdjustAudioSpeed(ctx context.Context, inputFile string, speed float64) error {
	tempFile := inputFile + ".tmp.mp3"
	filterChain := buildAtempoFilterChain(speed)

	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-i", inputFile,
		"-filter:a", filterChain,
		"-map", "0:a",
		"-map", "0:v?",
		"-map_metadata", "0",
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

	if err := os.Remove(inputFile); err != nil {
		return fmt.Errorf("failed to remove original file: %v", err)
	}
	if err := os.Rename(tempFile, inputFile); err != nil {
		return fmt.Errorf("failed to rename temp file: %v", err)
	}
	return nil
}

// ExtractThumbnail returns the embedded cover art as raw bytes plus the detected mime type.
func ExtractThumbnail(filepath string) ([]byte, string, error) {
	cmd := exec.Command("ffmpeg", "-i", filepath, "-an", "-c:v", "copy", "-f", "image2pipe", "-")
	output, err := cmd.Output()
	if err != nil || len(output) == 0 {
		return nil, "", fmt.Errorf("no thumbnail found")
	}
	mimetype := "image/jpeg"
	if len(output) >= 8 && string(output[:8]) == "\x89PNG\r\n\x1a\n" {
		mimetype = "image/png"
	}
	return output, mimetype, nil
}

// CheckMP3HasArtwork uses ffprobe to detect an embedded video stream (cover art).
func CheckMP3HasArtwork(filepath string) bool {
	cmd := exec.Command("ffprobe", "-v", "quiet", "-select_streams", "v:0",
		"-show_entries", "stream=codec_name", "-of", "default=noprint_wrappers=1:nokey=1", filepath)
	output, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(output)) != ""
}
