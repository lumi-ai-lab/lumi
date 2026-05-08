package cron

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

type Store struct {
	path string
	mu   sync.RWMutex
}

func NewStore(path string) *Store {
	if path == "" {
		path = defaultPath()
	}
	_ = os.MkdirAll(filepath.Dir(path), 0755)
	return &Store{path: path}
}

func defaultPath() string {
	home := os.Getenv("HOME")
	if home == "" {
		home = os.Getenv("USERPROFILE")
	}
	if home == "" {
		home = "."
	}
	return filepath.Join(home, ".lumi", "cron", "jobs.json")
}

func (s *Store) List() ([]Job, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.loadLocked()
}

func (s *Store) SaveAll(jobs []Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked(jobs)
}

func (s *Store) Upsert(job Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	jobs, err := s.loadLocked()
	if err != nil {
		return err
	}
	found := false
	for i := range jobs {
		if jobs[i].ID == job.ID {
			jobs[i] = job
			found = true
			break
		}
	}
	if !found {
		jobs = append(jobs, job)
	}
	return s.saveLocked(jobs)
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	jobs, err := s.loadLocked()
	if err != nil {
		return err
	}
	next := jobs[:0]
	for _, job := range jobs {
		if job.ID != id {
			next = append(next, job)
		}
	}
	return s.saveLocked(next)
}

func (s *Store) loadLocked() ([]Job, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return []Job{}, nil
		}
		return nil, err
	}
	var jobs []Job
	if len(data) == 0 {
		return []Job{}, nil
	}
	if err := json.Unmarshal(data, &jobs); err != nil {
		return nil, err
	}
	sort.Slice(jobs, func(i, j int) bool {
		return jobs[i].CreatedAt > jobs[j].CreatedAt
	})
	return jobs, nil
}

func (s *Store) saveLocked(jobs []Job) error {
	data, err := json.MarshalIndent(jobs, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0755); err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0644)
}
