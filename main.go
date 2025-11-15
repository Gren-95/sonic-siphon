package main

import (
	"log"
	"os"

	"github.com/gin-gonic/gin"
)

const (
	TEMP_DIR   = "/temp"
	OUTPUT_DIR = "/output"
)

func main() {
	// Create directories if they don't exist
	if err := os.MkdirAll(TEMP_DIR, 0755); err != nil {
		log.Fatalf("Failed to create temp directory: %v", err)
	}
	if err := os.MkdirAll(OUTPUT_DIR, 0755); err != nil {
		log.Fatalf("Failed to create output directory: %v", err)
	}

	// Set Gin to release mode for production
	gin.SetMode(gin.ReleaseMode)
	r := gin.Default()

	// Serve static files
	r.Static("/static", "./static")
	r.StaticFile("/favicon.svg", "./static/favicon.svg")
	r.StaticFile("/manifest.json", "./static/manifest.json")

	// Routes
	r.GET("/", func(c *gin.Context) {
		c.File("./templates/index.html")
	})
	r.POST("/preview", previewHandler)
	r.POST("/download", downloadHandler)
	r.GET("/status/:id", statusHandler)
	r.GET("/jobs", listJobsHandler)
	r.DELETE("/jobs/:id", cancelJobHandler)
	r.GET("/files", listFilesHandler)
	r.GET("/thumbnail/:location/*filename", thumbnailHandler)
	r.DELETE("/delete/:location/*filename", deleteFileHandler)
	r.POST("/move", moveFilesHandler)
	r.GET("/stream/:location/*filename", streamFileHandler)

	// Start server
	log.Println("Starting server on :5000")
	if err := r.Run(":5000"); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

