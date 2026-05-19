package mcpstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// CurrentVersion is the schema version embedded in newly written files.
const CurrentVersion = 1

// File is the on-disk JSON schema for ~/.lumi/mcp.json.
type File struct {
	Version int      `json:"version"`
	Servers []Record `json:"servers"`
}

// Store provides goroutine-safe CRUD over the SSOT MCP file.
type Store struct {
	path string
	mu   sync.RWMutex
	file File
}

// New returns a Store rooted at the given absolute path. The file is created
// on first Save.
func New(path string) *Store {
	return &Store{path: path, file: File{Version: CurrentVersion}}
}

// Default returns the conventional ~/.lumi/mcp.json store, or an error if the
// home directory cannot be resolved.
func Default() (*Store, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home: %w", err)
	}
	return New(filepath.Join(home, ".lumi", "mcp.json")), nil
}

// Path returns the file path backing the store.
func (s *Store) Path() string { return s.path }

// Load reads the SSOT file from disk. Missing file is treated as an empty store.
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
	var parsed File
	if len(data) == 0 {
		s.file = File{Version: CurrentVersion}
		return nil
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return fmt.Errorf("parse %s: %w", s.path, err)
	}
	if parsed.Version == 0 {
		parsed.Version = CurrentVersion
	}
	s.file = parsed
	return nil
}

// List returns a copy of all records sorted by name.
func (s *Store) List() []Record {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Record, len(s.file.Servers))
	for i, r := range s.file.Servers {
		out[i] = r.Clone()
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Get returns the record with the given id (and a found flag).
func (s *Store) Get(id string) (Record, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, r := range s.file.Servers {
		if r.ID == id {
			return r.Clone(), true
		}
	}
	return Record{}, false
}

// Upsert inserts or replaces a record by id, validating it first. Pass an
// empty id to have the store generate one based on name + timestamp.
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
	for i := range s.file.Servers {
		if s.file.Servers[i].ID == r.ID {
			r.CreatedAt = s.file.Servers[i].CreatedAt
			s.file.Servers[i] = r
			if err := s.persistLocked(); err != nil {
				return Record{}, err
			}
			return r.Clone(), nil
		}
	}
	s.file.Servers = append(s.file.Servers, r)
	if err := s.persistLocked(); err != nil {
		return Record{}, err
	}
	return r.Clone(), nil
}

// Delete removes the record matching id. Returns true if a record was removed.
func (s *Store) Delete(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.file.Servers {
		if s.file.Servers[i].ID == id {
			s.file.Servers = append(s.file.Servers[:i], s.file.Servers[i+1:]...)
			if err := s.persistLocked(); err != nil {
				return false, err
			}
			return true, nil
		}
	}
	return false, nil
}

// persistLocked atomically writes the file. Caller must hold the write lock.
func (s *Store) persistLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(s.path), err)
	}
	s.file.Version = CurrentVersion
	if s.file.Servers == nil {
		s.file.Servers = []Record{}
	}
	data, err := json.MarshalIndent(s.file, "", "  ")
	if err != nil {
		return fmt.Errorf("encode mcp store: %w", err)
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

func errInvalidf(format string, args ...any) error {
	return &validationError{msg: fmt.Sprintf(format, args...)}
}

type validationError struct{ msg string }

func (e *validationError) Error() string { return e.msg }

// IsValidationError reports whether err originated from Record.Validate.
func IsValidationError(err error) bool {
	var v *validationError
	return errors.As(err, &v)
}

func generateID(name string, ts int64) string {
	slug := slugify(name)
	if slug == "" {
		slug = "mcp"
	}
	return fmt.Sprintf("%s-%d", slug, ts)
}
