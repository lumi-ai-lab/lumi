package requestercontext

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pengmide/lumi/internal/fssecure"
)

const DefaultTTL = 30 * time.Minute

// fileBridgePathMu makes publish and cleanup atomic with respect to every
// FileBridge in this process, including independently constructed bridges that
// target the same ACP session file.
var fileBridgePathMu sync.Mutex

// Envelope is the on-disk session-to-requester binding read by agent hooks.
type Envelope struct {
	Version          int       `json:"version"`
	WorkspaceID      string    `json:"workspaceId"`
	AgentID          string    `json:"agentId"`
	SessionID        string    `json:"sessionId"`
	IssuedAt         time.Time `json:"issuedAt"`
	ExpiresAt        time.Time `json:"expiresAt"`
	RequesterContext Context   `json:"requesterContext"`
}

// CleanupFunc removes a session context file. A cleanup function returned by
// FileBridge.Write is safe to call more than once.
type CleanupFunc func() error

// FileBridge writes session-scoped requester context files beneath a private
// workspace/agent directory.
type FileBridge struct {
	dir         string
	workspaceID string
	agentID     string
	ttl         time.Duration
	now         func() time.Time
	dirMode     os.FileMode
	fileMode    os.FileMode
	readerGID   *uint32
}

// FileBridgeOption customizes a FileBridge.
type FileBridgeOption func(*FileBridge) error

// WithTTL changes the lifetime recorded in newly written envelopes.
func WithTTL(ttl time.Duration) FileBridgeOption {
	return func(bridge *FileBridge) error {
		if ttl <= 0 {
			return fmt.Errorf("requester context TTL must be positive")
		}
		bridge.ttl = ttl
		return nil
	}
}

// WithClock replaces the wall clock used by FileBridge. It is intended for
// deterministic tests.
func WithClock(now func() time.Time) FileBridgeOption {
	return func(bridge *FileBridge) error {
		if now == nil {
			return fmt.Errorf("requester context clock must not be nil")
		}
		bridge.now = now
		return nil
	}
}

// WithReaderGID grants one deployment-managed group traversal access to the
// context directory and read access to context files. The publisher remains
// the owner; numeric IDs are supplied by deployment configuration.
func WithReaderGID(gid uint32) FileBridgeOption {
	return func(bridge *FileBridge) error {
		if gid == 0 {
			return fmt.Errorf("requester context reader GID must not be root")
		}
		bridge.readerGID = &gid
		bridge.dirMode = 0o710
		bridge.fileMode = 0o640
		return nil
	}
}

// NewFileBridge constructs a bridge without touching the filesystem.
func NewFileBridge(baseRoot, workspaceID, agentID string, options ...FileBridgeOption) (*FileBridge, error) {
	return newFileBridge(baseRoot, workspaceID, workspaceID, agentID, options...)
}

// NewFileBridgeInScope constructs a bridge whose files live beneath a stable
// directory scope while its envelopes retain the actual workspace identity.
// This is useful for a long-lived agent process that can serve more than one
// workspace but receives its context directory through a process environment
// variable that cannot change after startup.
func NewFileBridgeInScope(baseRoot, directoryScope, workspaceID, agentID string, options ...FileBridgeOption) (*FileBridge, error) {
	return newFileBridge(baseRoot, directoryScope, workspaceID, agentID, options...)
}

func newFileBridge(baseRoot, directoryScope, workspaceID, agentID string, options ...FileBridgeOption) (*FileBridge, error) {
	if err := validatePathSegment("workspace ID", workspaceID); err != nil {
		return nil, err
	}
	if err := validatePathSegment("directory scope", directoryScope); err != nil {
		return nil, err
	}
	dir, err := SessionDir(baseRoot, directoryScope, agentID)
	if err != nil {
		return nil, err
	}

	bridge := &FileBridge{
		dir:         dir,
		workspaceID: workspaceID,
		agentID:     agentID,
		ttl:         DefaultTTL,
		now:         time.Now,
		dirMode:     0o700,
		fileMode:    0o600,
	}
	for _, option := range options {
		if option == nil {
			return nil, fmt.Errorf("requester context bridge option must not be nil")
		}
		if err := option(bridge); err != nil {
			return nil, err
		}
	}
	return bridge, nil
}

// Dir returns the absolute directory containing this bridge's session files.
func (bridge *FileBridge) Dir() string {
	return bridge.dir
}

// SessionDir returns a safe absolute directory rooted beneath baseRoot.
// Workspace and agent identifiers must each be a single path segment.
func SessionDir(baseRoot, workspaceID, agentID string) (string, error) {
	if strings.TrimSpace(baseRoot) == "" {
		return "", fmt.Errorf("requester context base root must not be empty")
	}
	if err := validatePathSegment("workspace ID", workspaceID); err != nil {
		return "", err
	}
	if err := validatePathSegment("agent ID", agentID); err != nil {
		return "", err
	}

	root, err := filepath.Abs(baseRoot)
	if err != nil {
		return "", fmt.Errorf("resolve requester context base root: %w", err)
	}
	root = filepath.Clean(root)
	dir := filepath.Join(root, workspaceID, agentID)
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return "", fmt.Errorf("verify requester context directory: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("requester context directory escapes base root")
	}
	return dir, nil
}

// SessionFileName returns the deterministic, path-safe filename for an ACP
// session identifier.
func SessionFileName(sessionID string) (string, error) {
	if sessionID == "" {
		return "", fmt.Errorf("ACP session ID must not be empty")
	}
	sum := sha256.Sum256([]byte(sessionID))
	return hex.EncodeToString(sum[:]) + ".json", nil
}

// Write atomically writes an envelope for sessionID and returns its path and an
// idempotent cleanup function. A stale cleanup does not remove a newer file for
// the same session.
func (bridge *FileBridge) Write(sessionID string, requester Context) (string, CleanupFunc, error) {
	if bridge == nil {
		return "", nil, fmt.Errorf("requester context file bridge must not be nil")
	}
	filename, err := SessionFileName(sessionID)
	if err != nil {
		return "", nil, err
	}
	if err := bridge.ensureDir(); err != nil {
		return "", nil, err
	}

	issuedAt := bridge.now().UTC()
	envelope := Envelope{
		Version:          CurrentEnvelopeVersion,
		WorkspaceID:      bridge.workspaceID,
		AgentID:          bridge.agentID,
		SessionID:        sessionID,
		IssuedAt:         issuedAt,
		ExpiresAt:        issuedAt.Add(bridge.ttl),
		RequesterContext: requester,
	}
	data, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return "", nil, fmt.Errorf("encode requester context envelope: %w", err)
	}
	data = append(data, '\n')

	path := filepath.Join(bridge.dir, filename)
	temporary, err := os.CreateTemp(bridge.dir, ".requester-context-*.tmp")
	if err != nil {
		return "", nil, fmt.Errorf("create requester context temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	keepTemporary := true
	defer func() {
		if keepTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()

	if err := temporary.Chmod(bridge.fileMode); err != nil {
		_ = temporary.Close()
		return "", nil, fmt.Errorf("set requester context temporary file permissions: %w", err)
	}
	if err := setFileGroup(temporaryPath, bridge.readerGID); err != nil {
		_ = temporary.Close()
		return "", nil, fmt.Errorf("set requester context temporary file group: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return "", nil, fmt.Errorf("write requester context temporary file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return "", nil, fmt.Errorf("sync requester context temporary file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return "", nil, fmt.Errorf("close requester context temporary file: %w", err)
	}
	writtenInfo, err := bridge.publishRequesterContextFile(temporaryPath, path)
	if err != nil {
		return "", nil, err
	}
	keepTemporary = false

	var once sync.Once
	var cleanupErr error
	cleanup := CleanupFunc(func() error {
		once.Do(func() {
			fileBridgePathMu.Lock()
			defer fileBridgePathMu.Unlock()
			currentInfo, statErr := os.Stat(path)
			switch {
			case errors.Is(statErr, os.ErrNotExist):
				return
			case statErr != nil:
				cleanupErr = fmt.Errorf("inspect requester context file for cleanup: %w", statErr)
				return
			case !os.SameFile(writtenInfo, currentInfo):
				return
			}
			if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				cleanupErr = fmt.Errorf("remove requester context file: %w", removeErr)
			}
		})
		return cleanupErr
	})
	return path, cleanup, nil
}

func (bridge *FileBridge) publishRequesterContextFile(temporaryPath, path string) (os.FileInfo, error) {
	fileBridgePathMu.Lock()
	defer fileBridgePathMu.Unlock()

	if err := os.Rename(temporaryPath, path); err != nil {
		return nil, fmt.Errorf("publish requester context file: %w", err)
	}
	if err := os.Chmod(path, bridge.fileMode); err != nil {
		_ = os.Remove(path)
		return nil, fmt.Errorf("set requester context file permissions: %w", err)
	}
	if err := setFileGroup(path, bridge.readerGID); err != nil {
		_ = os.Remove(path)
		return nil, fmt.Errorf("set requester context file group: %w", err)
	}
	writtenInfo, err := os.Stat(path)
	if err != nil {
		_ = os.Remove(path)
		return nil, fmt.Errorf("inspect requester context file: %w", err)
	}
	return writtenInfo, nil
}

func (bridge *FileBridge) ensureDir() error {
	workspaceDir := filepath.Dir(bridge.dir)
	contextRoot := filepath.Dir(workspaceDir)
	for _, dir := range []string{contextRoot, workspaceDir, bridge.dir} {
		if bridge.readerGID != nil {
			if err := fssecure.EnsureDirectory(dir, bridge.dirMode, bridge.readerGID); err != nil {
				return fmt.Errorf("prepare requester context directory: %w", err)
			}
			continue
		}
		if err := os.MkdirAll(dir, bridge.dirMode); err != nil {
			return fmt.Errorf("create private requester context directory %q: %w", dir, err)
		}
		info, err := os.Lstat(dir)
		if err != nil {
			return fmt.Errorf("inspect private requester context directory %q: %w", dir, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("requester context directory %q must be a real directory", dir)
		}
		if err := os.Chmod(dir, bridge.dirMode); err != nil {
			return fmt.Errorf("set private requester context directory mode %q: %w", dir, err)
		}
	}
	return nil
}

func setFileGroup(path string, gid *uint32) error {
	return fssecure.SetGroup(path, gid)
}

func validatePathSegment(label, value string) error {
	if value == "" {
		return fmt.Errorf("requester context %s must not be empty", label)
	}
	if value == "." || value == ".." {
		return fmt.Errorf("requester context %s %q is not a safe path segment", label, value)
	}
	if strings.ContainsAny(value, "/\\\x00") || filepath.IsAbs(value) || filepath.VolumeName(value) != "" {
		return fmt.Errorf("requester context %s %q is not a safe path segment", label, value)
	}
	if filepath.Clean(value) != value || filepath.Base(value) != value {
		return fmt.Errorf("requester context %s %q is not a safe path segment", label, value)
	}
	return nil
}
