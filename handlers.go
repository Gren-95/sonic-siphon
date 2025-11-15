package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type DownloadStatus struct {
	ID        string    `json:"id"`
	Status    string    `json:"status"`
	Message   string    `json:"message"`
	Progress  string    `json:"progress"`
	URL       string    `json:"url,omitempty"`
	Speed     float64   `json:"speed,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	cancel    context.CancelFunc
	cmd       *exec.Cmd
}

var (
	downloads = make(map[string]*DownloadStatus)
	mu        sync.RWMutex
)


func previewHandler(c *gin.Context) {
	var req struct {
		URL string `json:"url"`
	}

	if err := c.ShouldBindJSON(&req); err != nil || req.URL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "URL is required"})
		return
	}

	info, err := getVideoInfo(req.URL)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, info)
}

func downloadHandler(c *gin.Context) {
	var req struct {
		URL   string  `json:"url"`
		Speed float64 `json:"speed"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
		return
	}

	if req.URL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No URL provided"})
		return
	}

	if req.Speed == 0 {
		req.Speed = 1.0
	}

	downloadID := uuid.New().String()
	ctx, cancel := context.WithCancel(context.Background())
	
	mu.Lock()
	downloads[downloadID] = &DownloadStatus{
		ID:        downloadID,
		Status:    "queued",
		Message:   "Starting download...",
		Progress:  "0%",
		URL:       req.URL,
		Speed:     req.Speed,
		CreatedAt: time.Now(),
		cancel:    cancel,
	}
	mu.Unlock()

	// Start download in goroutine
	go downloadTask(ctx, req.URL, req.Speed, downloadID)

	c.JSON(http.StatusOK, gin.H{"download_id": downloadID})
}

func statusHandler(c *gin.Context) {
	downloadID := c.Param("id")
	mu.RLock()
	status, exists := downloads[downloadID]
	mu.RUnlock()

	if !exists {
		c.JSON(http.StatusNotFound, gin.H{"error": "Download ID not found"})
		return
	}

	c.JSON(http.StatusOK, status)
}

func listFilesHandler(c *gin.Context) {
	tempFiles, err := getFilesInDir(TEMP_DIR, "temp")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	outputFiles, err := getFilesInDir(OUTPUT_DIR, "output")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"temp_files":   tempFiles,
		"output_files": outputFiles,
	})
}

func thumbnailHandler(c *gin.Context) {
	location := c.Param("location")
	filename := strings.TrimPrefix(c.Param("filename"), "/")

	var baseDir string
	if location == "temp" {
		baseDir = TEMP_DIR
	} else if location == "output" {
		baseDir = OUTPUT_DIR
	} else {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid location"})
		return
	}

	filepath := filepath.Join(baseDir, filename)
	if !strings.HasPrefix(filepath, baseDir) {
		c.JSON(http.StatusNotFound, gin.H{"error": "File not found"})
		return
	}

	// Extract thumbnail using ffmpeg
	cmd := exec.Command("ffmpeg", "-i", filepath, "-an", "-c:v", "copy", "-f", "image2pipe", "-")
	output, err := cmd.Output()
	if err != nil || len(output) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "No thumbnail found"})
		return
	}

	// Determine MIME type
	mimetype := "image/jpeg"
	if len(output) >= 8 && string(output[:8]) == "\x89PNG\r\n\x1a\n" {
		mimetype = "image/png"
	}

	c.Data(http.StatusOK, mimetype, output)
}

func deleteFileHandler(c *gin.Context) {
	location := c.Param("location")
	filename := strings.TrimPrefix(c.Param("filename"), "/")

	var baseDir string
	if location == "temp" {
		baseDir = TEMP_DIR
	} else if location == "output" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Cannot delete from output directory"})
		return
	} else {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid location"})
		return
	}

	filepath := filepath.Join(baseDir, filename)
	if !strings.HasPrefix(filepath, baseDir) {
		c.JSON(http.StatusNotFound, gin.H{"error": "File not found"})
		return
	}

	if err := os.Remove(filepath); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

func moveFilesHandler(c *gin.Context) {
	var req struct {
		Filenames []string `json:"filenames"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
		return
	}

	if len(req.Filenames) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No files specified"})
		return
	}

	movedCount := 0
	errors := []string{}

	for _, filename := range req.Filenames {
		srcPath := filepath.Join(TEMP_DIR, filename)
		dstPath := filepath.Join(OUTPUT_DIR, filename)

		if !strings.HasPrefix(srcPath, TEMP_DIR) || !strings.HasPrefix(dstPath, OUTPUT_DIR) {
			errors = append(errors, fmt.Sprintf("%s: Invalid path", filename))
			continue
		}

		if _, err := os.Stat(srcPath); os.IsNotExist(err) {
			errors = append(errors, fmt.Sprintf("%s: File not found in temp", filename))
			continue
		}

		// Try rename first (fast for same filesystem)
		err := os.Rename(srcPath, dstPath)
		if err != nil {
			// If rename fails due to cross-device link, copy and delete
			if linkErr, ok := err.(*os.LinkError); ok {
				if linkErr.Err == syscall.EXDEV {
					// Cross-device link error - use copy instead
					if copyErr := copyFile(srcPath, dstPath); copyErr != nil {
						errors = append(errors, fmt.Sprintf("%s: %v", filename, copyErr))
						continue
					}
					// Remove source file after successful copy
					if removeErr := os.Remove(srcPath); removeErr != nil {
						errors = append(errors, fmt.Sprintf("%s: copied but failed to remove source: %v", filename, removeErr))
						// Continue anyway since file was copied
					}
				} else {
					errors = append(errors, fmt.Sprintf("%s: %v", filename, err))
					continue
				}
			} else {
				errors = append(errors, fmt.Sprintf("%s: %v", filename, err))
				continue
			}
		}

		movedCount++
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"moved":   movedCount,
		"errors":  errors,
	})
}

// copyFile copies a file from src to dst
func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	if err != nil {
		return err
	}

	// Sync to ensure data is written to disk
	return destFile.Sync()
}

func streamFileHandler(c *gin.Context) {
	location := c.Param("location")
	filename := strings.TrimPrefix(c.Param("filename"), "/")

	var baseDir string
	if location == "temp" {
		baseDir = TEMP_DIR
	} else if location == "output" {
		baseDir = OUTPUT_DIR
	} else {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid location"})
		return
	}

	filepath := filepath.Join(baseDir, filename)
	if !strings.HasPrefix(filepath, baseDir) {
		c.JSON(http.StatusNotFound, gin.H{"error": "File not found"})
		return
	}

	c.File(filepath)
}

func listJobsHandler(c *gin.Context) {
	mu.RLock()
	jobs := make([]*DownloadStatus, 0, len(downloads))
	for _, job := range downloads {
		// Create a copy without internal fields
		jobs = append(jobs, &DownloadStatus{
			ID:        job.ID,
			Status:    job.Status,
			Message:   job.Message,
			Progress:  job.Progress,
			URL:       job.URL,
			Speed:     job.Speed,
			CreatedAt: job.CreatedAt,
		})
	}
	mu.RUnlock()

	// Sort by creation time, newest first (using sort.Slice for efficiency)
	sort.Slice(jobs, func(i, j int) bool {
		return jobs[i].CreatedAt.After(jobs[j].CreatedAt)
	})

	c.JSON(http.StatusOK, gin.H{"jobs": jobs})
}

func cancelJobHandler(c *gin.Context) {
	jobID := c.Param("id")
	
	mu.Lock()
	job, exists := downloads[jobID]
	if !exists {
		mu.Unlock()
		c.JSON(http.StatusNotFound, gin.H{"error": "Job not found"})
		return
	}

	// Only allow canceling active jobs
	if job.Status != "queued" && job.Status != "downloading" && job.Status != "processing" {
		mu.Unlock()
		c.JSON(http.StatusBadRequest, gin.H{"error": "Job cannot be cancelled"})
		return
	}

	// Cancel the context
	if job.cancel != nil {
		job.cancel()
	}

	// Kill the command if it exists
	if job.cmd != nil && job.cmd.Process != nil {
		job.cmd.Process.Kill()
	}

	// Update status
	job.Status = "cancelled"
	job.Message = "Job cancelled by user"
	mu.Unlock()

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "Job cancelled"})
}

