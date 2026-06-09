package sandbox

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/pengmide/lumi/internal/lumipaths"
)

type Store struct {
	path string
	mu   sync.Mutex
}

type persistedSandboxes struct {
	Sandboxes []RuntimeRecord `json:"sandboxes"`
}

func NewStore(path string) *Store {
	if path == "" {
		path = defaultStorePath()
	}
	return &Store{path: path}
}

func defaultStorePath() string {
	return lumipaths.Path("runtime", "sandboxes.json")
}

func DefaultRuntimeDir() string {
	return filepath.Dir(defaultStorePath())
}

func (s *Store) Load() ([]RuntimeRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []RuntimeRecord{}, nil
		}
		return nil, err
	}

	var file persistedSandboxes
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, err
	}
	if file.Sandboxes == nil {
		return []RuntimeRecord{}, nil
	}
	return file.Sandboxes, nil
}

func (s *Store) Save(records []RuntimeRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(persistedSandboxes{Sandboxes: records}, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	tmpPath := s.path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpPath, s.path)
}
