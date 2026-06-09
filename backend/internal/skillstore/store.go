package skillstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/pengmide/lumi/internal/lumipaths"
)

// CurrentVersion is the schema version embedded in newly written files.
const CurrentVersion = 1

// File is the on-disk JSON schema for ~/.lumi/skills.json.
type File struct {
	Version int      `json:"version"`
	Skills  []Record `json:"skills"`
}

// Store provides goroutine-safe CRUD over the SSOT skills file.
type Store struct {
	path     string
	cacheDir string
	archDir  string
	mu       sync.RWMutex
	file     File
}

// New returns a Store backed by the given absolute file path; cacheDir is the
// root for materialized git/archive sources and archDir is where uploaded zip
// archives are persisted before extraction.
func New(path, cacheDir, archDir string) *Store {
	return &Store{
		path:     path,
		cacheDir: cacheDir,
		archDir:  archDir,
		file:     File{Version: CurrentVersion},
	}
}

// Default returns the conventional store rooted under ~/.lumi.
func Default() (*Store, error) {
	root := lumipaths.Home()
	return New(
		filepath.Join(root, "skills.json"),
		filepath.Join(root, "skills", "_cache"),
		filepath.Join(root, "skills", "_archives"),
	), nil
}

// Path returns the SSOT file path.
func (s *Store) Path() string { return s.path }

// CacheDir returns the directory where git/archive sources are materialized.
func (s *Store) CacheDir() string { return s.cacheDir }

// ArchiveDir returns the directory where uploaded archives are persisted.
func (s *Store) ArchiveDir() string { return s.archDir }

// Load reads the SSOT file from disk. A missing file is treated as empty.
func (s *Store) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		s.file = File{Version: CurrentVersion}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", s.path, err)
	}
	if len(data) == 0 {
		s.file = File{Version: CurrentVersion}
		return nil
	}
	var parsed File
	if err := json.Unmarshal(data, &parsed); err != nil {
		return fmt.Errorf("parse %s: %w", s.path, err)
	}
	if parsed.Version == 0 {
		parsed.Version = CurrentVersion
	}
	s.file = parsed
	return nil
}

// List returns a snapshot of all records sorted by name.
func (s *Store) List() []Record {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Record, len(s.file.Skills))
	copy(out, s.file.Skills)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Get returns the record with the given id.
func (s *Store) Get(id string) (Record, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, r := range s.file.Skills {
		if r.ID == id {
			return r, true
		}
	}
	return Record{}, false
}

// Upsert inserts or replaces a record by id.
func (s *Store) Upsert(r Record) (Record, error) {
	now := time.Now().UnixMilli()
	if r.ID == "" {
		r.ID = generateID(r.Name, now)
	}
	if r.CreatedAt == 0 {
		r.CreatedAt = now
	}
	r.UpdatedAt = now
	if err := r.Validate(); err != nil {
		return Record{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.file.Skills {
		if s.file.Skills[i].ID == r.ID {
			r.CreatedAt = s.file.Skills[i].CreatedAt
			s.file.Skills[i] = r
			if err := s.persistLocked(); err != nil {
				return Record{}, err
			}
			return r, nil
		}
	}
	s.file.Skills = append(s.file.Skills, r)
	if err := s.persistLocked(); err != nil {
		return Record{}, err
	}
	return r, nil
}

// Delete removes the record matching id.
func (s *Store) Delete(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.file.Skills {
		if s.file.Skills[i].ID == id {
			s.file.Skills = append(s.file.Skills[:i], s.file.Skills[i+1:]...)
			if err := s.persistLocked(); err != nil {
				return false, err
			}
			return true, nil
		}
	}
	return false, nil
}

func (s *Store) persistLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(s.path), err)
	}
	s.file.Version = CurrentVersion
	if s.file.Skills == nil {
		s.file.Skills = []Record{}
	}
	data, err := json.MarshalIndent(s.file, "", "  ")
	if err != nil {
		return fmt.Errorf("encode skill store: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write tmp %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("rename %s -> %s: %w", tmp, s.path, err)
	}
	return nil
}

// IsValidationError reports whether err originated from Record.Validate.
func IsValidationError(err error) bool {
	var v *validationError
	return errors.As(err, &v)
}
