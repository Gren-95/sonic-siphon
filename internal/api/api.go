package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humagin"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Config holds runtime paths used by the API.
type Config struct {
	TempDir   string
	OutputDir string
}

// API bundles the JobStore and config so handlers can share state.
type API struct {
	store *JobStore
	cfg   Config
}

// New constructs the API and registers all routes (huma + raw gin file streaming) under /api/v1.
func New(r *gin.Engine, cfg Config) *API {
	a := &API{store: NewJobStore(), cfg: cfg}

	humaCfg := huma.DefaultConfig("Sonic Siphon API", "1.0.0")
	humaCfg.Info.Description = "YouTube audio downloader. Preview videos/playlists, queue downloads, " +
		"adjust playback speed via ffmpeg, and manage temp/output libraries. Designed to be agent-friendly: " +
		"every endpoint is documented in this OpenAPI spec."
	// Paths are relative to the gin group prefix (/api/v1).
	humaCfg.OpenAPIPath = "/openapi"
	humaCfg.DocsPath = "/docs"
	humaCfg.SchemasPath = "/schemas"
	humaCfg.Servers = []*huma.Server{{URL: "/api/v1", Description: "This server"}}

	apiGroup := r.Group("/api/v1")
	api := humagin.NewWithGroup(r, apiGroup, humaCfg)

	a.registerOperations(api)
	a.registerFileRoutes(apiGroup)

	return a
}

func (a *API) registerOperations(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "health",
		Method:      http.MethodGet,
		Path:        "/health",
		Summary:     "Liveness probe",
		Description: "Returns 200 with basic status info. Use as a docker/k8s healthcheck. " +
			"Does not exercise yt-dlp or ffmpeg — those failures surface as job errors instead.",
		Tags: []string{"system"},
	}, a.Health)

	huma.Register(api, huma.Operation{
		OperationID: "preview",
		Method:      http.MethodPost,
		Path:        "/preview",
		Summary:     "Preview a video or playlist",
		Description: "Resolves a YouTube URL and returns metadata. For playlists, returns the count plus " +
			"a 3-video preview by default. Pass `full=true` in the body to fetch every video's ID, title, and duration " +
			"(useful for AI agents that need to filter playlists by content).",
		Tags: []string{"discovery"},
	}, a.Preview)

	huma.Register(api, huma.Operation{
		OperationID: "create-download",
		Method:      http.MethodPost,
		Path:        "/downloads",
		Summary:     "Queue a download",
		Description: "Starts a background download job. Pass either `url` (single video or full playlist) or " +
			"`video_ids` (subset of YouTube IDs). `speed` is a multiplier applied via ffmpeg's atempo filter " +
			"(0.5 = half speed, 2.0 = double; values outside that range are chained automatically). Returns immediately " +
			"with a job ID; poll /downloads/{id} for status.",
		Tags: []string{"downloads"},
	}, a.CreateDownload)

	huma.Register(api, huma.Operation{
		OperationID: "get-download",
		Method:      http.MethodGet,
		Path:        "/downloads/{id}",
		Summary:     "Get download status",
		Tags:        []string{"downloads"},
	}, a.GetDownload)

	huma.Register(api, huma.Operation{
		OperationID: "list-downloads",
		Method:      http.MethodGet,
		Path:        "/downloads",
		Summary:     "List download jobs",
		Description: "Returns all known jobs (newest first). State is in-memory and lost on restart.",
		Tags:        []string{"downloads"},
	}, a.ListDownloads)

	huma.Register(api, huma.Operation{
		OperationID: "cancel-download",
		Method:      http.MethodDelete,
		Path:        "/downloads/{id}",
		Summary:     "Cancel a download",
		Description: "Cancels a queued/active job. No-op if already finished.",
		Tags:        []string{"downloads"},
	}, a.CancelDownload)

	huma.Register(api, huma.Operation{
		OperationID: "list-files",
		Method:      http.MethodGet,
		Path:        "/files",
		Summary:     "List MP3 files",
		Description: "Returns MP3s in /temp (downloads pending review) and /output (curated library).",
		Tags:        []string{"files"},
	}, a.ListFiles)

	huma.Register(api, huma.Operation{
		OperationID: "delete-file",
		Method:      http.MethodDelete,
		Path:        "/files/{location}/{filename}",
		Summary:     "Delete a file",
		Description: "Permanently deletes the named MP3 from /temp or /output. Idempotent: missing files return success.",
		Tags:        []string{"files"},
	}, a.DeleteFile)

	huma.Register(api, huma.Operation{
		OperationID: "move-files",
		Method:      http.MethodPost,
		Path:        "/files/move",
		Summary:     "Move files from /temp to /output",
		Tags:        []string{"files"},
	}, a.MoveFiles)
}

func (a *API) registerFileRoutes(g *gin.RouterGroup) {
	// Streaming and thumbnail routes serve binary content; documented manually below.
	g.GET("/files/:location/stream/*filename", a.streamFile)
	g.GET("/files/:location/thumbnail/*filename", a.thumbnailFile)
}

// ---- typed huma handlers ----

type HealthOutput struct {
	Body struct {
		Status  string `json:"status" doc:"Always 'ok' if the server is responding"`
		Version string `json:"version" doc:"API version"`
	}
}

func (a *API) Health(ctx context.Context, _ *struct{}) (*HealthOutput, error) {
	out := &HealthOutput{}
	out.Body.Status = "ok"
	out.Body.Version = "1.0.0"
	return out, nil
}

type PreviewInput struct {
	Body struct {
		URL  string `json:"url" required:"true" maxLength:"2048" doc:"YouTube video or playlist URL"`
		Full bool   `json:"full,omitempty" doc:"For playlists, return every video's ID/title/duration instead of the 3-video preview"`
	}
}

type PreviewOutput struct {
	Body VideoInfo
}

func (a *API) Preview(ctx context.Context, in *PreviewInput) (*PreviewOutput, error) {
	if strings.TrimSpace(in.Body.URL) == "" {
		return nil, huma.Error400BadRequest("url is required")
	}
	if !IsAllowedSourceURL(in.Body.URL) {
		return nil, huma.Error400BadRequest("url host is not in the allowlist (only youtube.com / youtu.be / music.youtube.com)")
	}
	info, err := GetVideoInfo(in.Body.URL, in.Body.Full)
	if err != nil {
		return nil, huma.Error500InternalServerError(err.Error())
	}
	return &PreviewOutput{Body: *info}, nil
}

type CreateDownloadInput struct {
	Body struct {
		URL      string   `json:"url,omitempty" maxLength:"2048" doc:"YouTube URL (single video or playlist)"`
		VideoIDs []string `json:"video_ids,omitempty" maxItems:"100" doc:"Specific YouTube video IDs to download (alternative to url, max 100)"`
		Speed    float64  `json:"speed,omitempty" doc:"Speed multiplier (default 1.0). 0.5 = half, 2.0 = double."`
	}
}

type CreateDownloadOutput struct {
	Body struct {
		DownloadID string `json:"download_id"`
	}
}

func (a *API) CreateDownload(ctx context.Context, in *CreateDownloadInput) (*CreateDownloadOutput, error) {
	if strings.TrimSpace(in.Body.URL) == "" && len(in.Body.VideoIDs) == 0 {
		return nil, huma.Error400BadRequest("either url or video_ids is required")
	}
	if in.Body.URL != "" && !IsAllowedSourceURL(in.Body.URL) {
		return nil, huma.Error400BadRequest("url host is not in the allowlist (only youtube.com / youtu.be / music.youtube.com)")
	}
	speed := in.Body.Speed
	if speed == 0 {
		speed = 1.0
	}

	id := uuid.New().String()
	jobCtx, cancel := context.WithCancel(context.Background())

	a.store.Add(id, &DownloadStatus{
		ID:        id,
		Status:    "queued",
		Message:   "Starting download...",
		Progress:  "0%",
		URL:       in.Body.URL,
		Speed:     speed,
		CreatedAt: nowFn(),
		cancel:    cancel,
	})

	url := in.Body.URL
	if url == "" && len(in.Body.VideoIDs) > 0 {
		url = "https://www.youtube.com/watch?v=" + in.Body.VideoIDs[0]
	}

	go runDownloadTask(jobCtx, a.store, id, url, speed, a.cfg.TempDir, in.Body.VideoIDs)

	out := &CreateDownloadOutput{}
	out.Body.DownloadID = id
	return out, nil
}

type GetDownloadInput struct {
	ID string `path:"id" doc:"Job UUID returned from POST /downloads"`
}

type GetDownloadOutput struct {
	Body DownloadStatus
}

func (a *API) GetDownload(ctx context.Context, in *GetDownloadInput) (*GetDownloadOutput, error) {
	job, ok := a.store.Get(in.ID)
	if !ok {
		return nil, huma.Error404NotFound("download not found")
	}
	return &GetDownloadOutput{Body: *job}, nil
}

type ListDownloadsOutput struct {
	Body struct {
		Jobs []*DownloadStatus `json:"jobs"`
	}
}

func (a *API) ListDownloads(ctx context.Context, _ *struct{}) (*ListDownloadsOutput, error) {
	out := &ListDownloadsOutput{}
	out.Body.Jobs = a.store.Snapshot()
	return out, nil
}

type CancelDownloadInput struct {
	ID string `path:"id"`
}

type CancelDownloadOutput struct {
	Body struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
}

func (a *API) CancelDownload(ctx context.Context, in *CancelDownloadInput) (*CancelDownloadOutput, error) {
	ok, reason := a.store.Cancel(in.ID)
	if !ok {
		switch reason {
		case "not_found":
			return nil, huma.Error404NotFound("job not found")
		case "not_active":
			return nil, huma.Error400BadRequest("job cannot be cancelled")
		}
	}
	out := &CancelDownloadOutput{}
	out.Body.Success = true
	out.Body.Message = "Job cancelled"
	return out, nil
}

type ListFilesOutput struct {
	Body struct {
		TempFiles   []FileInfo `json:"temp_files"`
		OutputFiles []FileInfo `json:"output_files"`
	}
}

func (a *API) ListFiles(ctx context.Context, _ *struct{}) (*ListFilesOutput, error) {
	temp, err := ListFiles(a.cfg.TempDir, "temp")
	if err != nil {
		return nil, huma.Error500InternalServerError(err.Error())
	}
	output, err := ListFiles(a.cfg.OutputDir, "output")
	if err != nil {
		return nil, huma.Error500InternalServerError(err.Error())
	}
	out := &ListFilesOutput{}
	out.Body.TempFiles = temp
	out.Body.OutputFiles = output
	return out, nil
}

type DeleteFileInput struct {
	Location string `path:"location" enum:"temp,output" doc:"'temp' or 'output'"`
	Filename string `path:"filename" doc:"MP3 filename to delete"`
}

type DeleteFileOutput struct {
	Body struct {
		Success bool `json:"success"`
	}
}

func (a *API) DeleteFile(ctx context.Context, in *DeleteFileInput) (*DeleteFileOutput, error) {
	baseDir, ok := a.dirFor(in.Location)
	if !ok {
		return nil, huma.Error400BadRequest("invalid location")
	}
	full, err := SafeJoin(baseDir, in.Filename)
	if err != nil {
		return nil, huma.Error404NotFound("file not found")
	}
	if err := removeIfExists(full); err != nil {
		return nil, huma.Error500InternalServerError(err.Error())
	}
	out := &DeleteFileOutput{}
	out.Body.Success = true
	return out, nil
}

type MoveFilesInput struct {
	Body struct {
		Filenames []string `json:"filenames" required:"true" doc:"MP3 filenames in /temp to move into /output"`
	}
}

type MoveFilesOutput struct {
	Body struct {
		Success bool     `json:"success"`
		Moved   int      `json:"moved"`
		Errors  []string `json:"errors"`
	}
}

func (a *API) MoveFiles(ctx context.Context, in *MoveFilesInput) (*MoveFilesOutput, error) {
	if len(in.Body.Filenames) == 0 {
		return nil, huma.Error400BadRequest("no files specified")
	}
	moved, errs := MoveFiles(a.cfg.TempDir, a.cfg.OutputDir, in.Body.Filenames)
	out := &MoveFilesOutput{}
	out.Body.Success = true
	out.Body.Moved = moved
	out.Body.Errors = errs
	return out, nil
}

// ---- raw gin handlers (binary streaming) ----

func (a *API) streamFile(c *gin.Context) {
	location := c.Param("location")
	baseDir, ok := a.dirFor(location)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid location"})
		return
	}
	full, err := SafeJoin(baseDir, c.Param("filename"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "File not found"})
		return
	}
	c.File(full)
}

func (a *API) thumbnailFile(c *gin.Context) {
	location := c.Param("location")
	baseDir, ok := a.dirFor(location)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid location"})
		return
	}
	full, err := SafeJoin(baseDir, c.Param("filename"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "File not found"})
		return
	}
	data, mime, err := ExtractThumbnail(full)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.Data(http.StatusOK, mime, data)
}

func (a *API) dirFor(location string) (string, bool) {
	switch location {
	case "temp":
		return a.cfg.TempDir, true
	case "output":
		return a.cfg.OutputDir, true
	}
	return "", false
}
