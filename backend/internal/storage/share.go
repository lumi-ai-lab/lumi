package storage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/pengmide/lumi/internal/lumipaths"
)

type StoredShare struct {
	ID             string             `json:"id"`
	Token          string             `json:"token"`
	ConversationID string             `json:"conversationId"`
	WorkspaceID    string             `json:"workspaceId,omitempty"`
	Files          []StoredSharedFile `json:"files,omitempty"`
	CreatedAt      int64              `json:"createdAt"`
	UpdatedAt      int64              `json:"updatedAt"`
	RevokedAt      int64              `json:"revokedAt,omitempty"`
}

type StoredSharedFile struct {
	Path string `json:"path"`
}

type shareFile struct {
	Shares []StoredShare `json:"shares"`
}

type ShareStore struct {
	filePath string
	mu       sync.Mutex
}

func NewShareStore(filePath string) *ShareStore {
	if filePath == "" {
		filePath = defaultSharePath()
	}
	dir := filepath.Dir(filePath)
	_ = os.MkdirAll(dir, 0755)
	return &ShareStore{filePath: filePath}
}

func defaultSharePath() string {
	return lumipaths.Path("shares.json")
}

func (s *ShareStore) Load() []StoredShare {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadUnlocked()
}

func (s *ShareStore) loadUnlocked() []StoredShare {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		return nil
	}

	var file shareFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil
	}

	return append([]StoredShare(nil), file.Shares...)
}

func (s *ShareStore) saveUnlocked(shares []StoredShare) error {
	file := shareFile{Shares: shares}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.filePath, data, 0644)
}

func (s *ShareStore) GetActiveByConversation(conversationID string) *StoredShare {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, share := range s.loadUnlocked() {
		if share.ConversationID == conversationID && share.RevokedAt == 0 {
			copyShare := share
			return &copyShare
		}
	}

	return nil
}

func (s *ShareStore) GetActiveByToken(token string) *StoredShare {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, share := range s.loadUnlocked() {
		if share.Token == token && share.RevokedAt == 0 {
			copyShare := share
			return &copyShare
		}
	}

	return nil
}

func (s *ShareStore) Put(share StoredShare) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	shares := s.loadUnlocked()
	replaced := false
	for i := range shares {
		if shares[i].ID == share.ID {
			shares[i] = share
			replaced = true
			break
		}
	}
	if !replaced {
		shares = append(shares, share)
	}

	return s.saveUnlocked(shares)
}

func (s *ShareStore) RevokeByConversation(conversationID string, revokedAt int64) (*StoredShare, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	shares := s.loadUnlocked()
	for i := range shares {
		if shares[i].ConversationID == conversationID && shares[i].RevokedAt == 0 {
			shares[i].RevokedAt = revokedAt
			shares[i].UpdatedAt = revokedAt
			if err := s.saveUnlocked(shares); err != nil {
				return nil, err
			}
			copyShare := shares[i]
			return &copyShare, nil
		}
	}

	return nil, nil
}

func (s *ShareStore) RemoveByConversation(conversationID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	shares := s.loadUnlocked()
	filtered := make([]StoredShare, 0, len(shares))
	for _, share := range shares {
		if share.ConversationID != conversationID {
			filtered = append(filtered, share)
		}
	}

	return s.saveUnlocked(filtered)
}
