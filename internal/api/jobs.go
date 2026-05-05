package api

import (
	"context"
	"os/exec"
	"sort"
	"sync"
	"time"
)

type DownloadStatus struct {
	ID        string    `json:"id" doc:"Unique download job identifier"`
	Status    string    `json:"status" doc:"queued | downloading | processing | completed | error | cancelled"`
	Message   string    `json:"message" doc:"Human-readable status message"`
	Progress  string    `json:"progress" doc:"Progress percentage (e.g. '42%')"`
	URL       string    `json:"url,omitempty" doc:"Source URL"`
	Speed     float64   `json:"speed,omitempty" doc:"Speed multiplier applied to the audio"`
	CreatedAt time.Time `json:"created_at" doc:"When the job was created"`
	cancel    context.CancelFunc
	cmd       *exec.Cmd
}

type JobStore struct {
	mu   sync.RWMutex
	jobs map[string]*DownloadStatus
}

func NewJobStore() *JobStore {
	return &JobStore{jobs: make(map[string]*DownloadStatus)}
}

func (s *JobStore) Add(id string, job *DownloadStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs[id] = job
}

func (s *JobStore) Get(id string) (*DownloadStatus, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	job, ok := s.jobs[id]
	return job, ok
}

func (s *JobStore) Update(id string, fn func(*DownloadStatus)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if job, ok := s.jobs[id]; ok {
		fn(job)
	}
}

func (s *JobStore) Snapshot() []*DownloadStatus {
	s.mu.RLock()
	jobs := make([]*DownloadStatus, 0, len(s.jobs))
	for _, j := range s.jobs {
		jobs = append(jobs, &DownloadStatus{
			ID:        j.ID,
			Status:    j.Status,
			Message:   j.Message,
			Progress:  j.Progress,
			URL:       j.URL,
			Speed:     j.Speed,
			CreatedAt: j.CreatedAt,
		})
	}
	s.mu.RUnlock()
	sort.Slice(jobs, func(i, j int) bool {
		return jobs[i].CreatedAt.After(jobs[j].CreatedAt)
	})
	return jobs
}

func (s *JobStore) Cancel(id string) (bool, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[id]
	if !ok {
		return false, "not_found"
	}
	if job.Status != "queued" && job.Status != "downloading" && job.Status != "processing" {
		return false, "not_active"
	}
	if job.cancel != nil {
		job.cancel()
	}
	if job.cmd != nil && job.cmd.Process != nil {
		_ = job.cmd.Process.Kill()
	}
	job.Status = "cancelled"
	job.Message = "Job cancelled by user"
	return true, "ok"
}

func (s *JobStore) attachCmd(id string, cmd *exec.Cmd) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if job, ok := s.jobs[id]; ok {
		job.cmd = cmd
	}
}
