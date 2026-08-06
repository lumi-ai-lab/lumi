package requesterpolicy

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/pengmide/lumi/internal/requestercontext"
)

// DefaultRefreshInterval is the default policy reload period.
const DefaultRefreshInterval = 10 * time.Minute

// Snapshot is an immutable in-process policy view.
type Snapshot struct {
	Path      string
	Revision  string
	BotID     string
	LoadedAt  time.Time
	enabled   map[string]User
	enabledN  int
}

// EnabledUserCount returns how many enabled users are in the snapshot.
func (s *Snapshot) EnabledUserCount() int {
	if s == nil {
		return 0
	}
	return s.enabledN
}

// Lookup returns a cloned enabled user after trimming surrounding whitespace.
// User IDs remain case-sensitive. Unknown and disabled users return false.
func (s *Snapshot) Lookup(userID string) (User, bool) {
	if s == nil {
		return User{}, false
	}
	user, ok := s.enabled[strings.TrimSpace(userID)]
	if !ok {
		return User{}, false
	}
	return cloneUser(user), true
}

// BuildContext resolves an enabled user into a requester context.
func (s *Snapshot) BuildContext(userID, requestID, chatID, chatType string) (*requestercontext.Context, User, bool) {
	user, ok := s.Lookup(userID)
	if !ok {
		return nil, User{}, false
	}
	ctx := &requestercontext.Context{
		Version:        requestercontext.CurrentContextVersion,
		RequestID:      strings.TrimSpace(requestID),
		PolicyRevision: s.Revision,
		Principal: requestercontext.Principal{
			Channel:         "wecom",
			BotID:           s.BotID,
			CanonicalUserID: user.UserID,
			DisplayName:     user.DisplayName,
		},
		Audience: requestercontext.Audience{
			ChatID:   strings.TrimSpace(chatID),
			ChatType: strings.TrimSpace(chatType),
		},
		Authorization: user.Authorization.Clone(),
	}
	return ctx, user, true
}

// Info is a safe diagnostic view (no user permission payloads).
type Info struct {
	Path             string    `json:"path"`
	Revision         string    `json:"revision"`
	EnabledUserCount int       `json:"enabledUserCount"`
	LoadedAt         time.Time `json:"loadedAt"`
}

// Store holds a process-local policy snapshot with optional background refresh.
type Store struct {
	path       string
	runtimeBot string
	interval   time.Duration
	snap       atomic.Pointer[Snapshot]
	stopCh     chan struct{}
	stopped    atomic.Bool
}

// Options configures a Store.
type Options struct {
	Path            string
	RuntimeBotID    string
	RefreshInterval time.Duration // 0 disables periodic refresh; negative uses default
}

// NewStore loads the policy file into process memory.
func NewStore(opts Options) (*Store, error) {
	path := strings.TrimSpace(opts.Path)
	if path == "" {
		return nil, errors.New("requester config path is required")
	}
	botID := strings.TrimSpace(opts.RuntimeBotID)
	if botID == "" {
		return nil, errors.New("runtime bot id is required")
	}
	interval := opts.RefreshInterval
	if interval < 0 {
		interval = DefaultRefreshInterval
	}
	store := &Store{
		path:       path,
		runtimeBot: botID,
		interval:   interval,
		stopCh:     make(chan struct{}),
	}
	if _, err := store.Reload(); err != nil {
		return nil, err
	}
	if interval > 0 {
		go store.refreshLoop()
	}
	return store, nil
}

// Path returns the configured policy file path.
func (s *Store) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

// Snapshot returns the current immutable snapshot (may be nil only if store is nil).
func (s *Store) Snapshot() *Snapshot {
	if s == nil {
		return nil
	}
	return s.snap.Load()
}

// Info returns a safe diagnostic view.
func (s *Store) Info() (Info, error) {
	if s == nil {
		return Info{}, errors.New("requester policy store is not enabled")
	}
	snap := s.snap.Load()
	if snap == nil {
		return Info{}, errors.New("requester policy snapshot is empty")
	}
	return Info{
		Path:             snap.Path,
		Revision:         snap.Revision,
		EnabledUserCount: snap.EnabledUserCount(),
		LoadedAt:         snap.LoadedAt,
	}, nil
}

// Reload re-reads the policy file. On failure the previous snapshot is retained.
func (s *Store) Reload() (Info, error) {
	if s == nil {
		return Info{}, errors.New("requester policy store is not enabled")
	}
	snap, err := loadSnapshot(s.path, s.runtimeBot)
	if err != nil {
		return Info{}, err
	}
	prev := s.snap.Load()
	if prev != nil && prev.Revision == snap.Revision {
		return Info{
			Path:             prev.Path,
			Revision:         prev.Revision,
			EnabledUserCount: prev.EnabledUserCount(),
			LoadedAt:         prev.LoadedAt,
		}, nil
	}
	s.snap.Store(snap)
	return Info{
		Path:             snap.Path,
		Revision:         snap.Revision,
		EnabledUserCount: snap.EnabledUserCount(),
		LoadedAt:         snap.LoadedAt,
	}, nil
}

// Close stops background refresh.
func (s *Store) Close() {
	if s == nil || !s.stopped.CompareAndSwap(false, true) {
		return
	}
	close(s.stopCh)
}

func (s *Store) refreshLoop() {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			if _, err := s.Reload(); err != nil {
				// Keep previous snapshot; surface via log at call site if desired.
				_ = err
			}
		}
	}
}

func loadSnapshot(path, runtimeBotID string) (*Snapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("load requester config: %w", err)
	}
	document, err := DecodeDocument(data)
	if err != nil {
		return nil, err
	}
	if err := NormalizeAndValidate(&document, runtimeBotID); err != nil {
		return nil, err
	}
	hash := sha256.Sum256(data)
	snap := &Snapshot{
		Path:     path,
		Revision: "sha256:" + hex.EncodeToString(hash[:]),
		BotID:    runtimeBotID,
		LoadedAt: time.Now().UTC(),
		enabled:  make(map[string]User),
	}
	for _, user := range document.Users {
		if !user.Enabled {
			continue
		}
		snap.enabled[user.UserID] = cloneUser(user)
	}
	snap.enabledN = len(snap.enabled)
	return snap, nil
}
