package device

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

type persistedDevices struct {
	Devices []Device `json:"devices"`
}

func NewStore(path string) *Store {
	if path == "" {
		path = DefaultDevicesPath()
	}
	return &Store{path: path}
}

func DefaultDevicesPath() string {
	return lumipaths.Path("devices.json")
}

func (s *Store) Load() ([]Device, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []Device{}, nil
		}
		return nil, err
	}

	var file persistedDevices
	if err := json.Unmarshal(data, &file); err == nil {
		if file.Devices == nil {
			return []Device{}, nil
		}
		return file.Devices, nil
	}

	var devices []Device
	if err := json.Unmarshal(data, &devices); err != nil {
		return nil, err
	}
	return devices, nil
}

func (s *Store) Save(devices []Device) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(persistedDevices{Devices: devices}, "", "  ")
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
