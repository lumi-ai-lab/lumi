package wecom

import (
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"

	"github.com/pengmide/lumi/internal/requestercontext"
	"github.com/pengmide/lumi/internal/requesterpolicy"
)

const requesterUnauthorizedReplyText = "你暂未开通该机器人的使用权限，请联系管理员。"

// ValidateRequesterConfigOutsideWorkspace prevents sandboxed agents from rewriting policy sources.
func ValidateRequesterConfigOutsideWorkspace(requesterConfigPath, workspacePath string) error {
	if strings.TrimSpace(requesterConfigPath) == "" {
		return nil
	}
	inside, err := pathWithinDirectory(requesterConfigPath, workspacePath)
	if err != nil {
		return fmt.Errorf("validate requester config location: %w", err)
	}
	if inside {
		return errors.New("requester config must be outside the sandbox workspace")
	}
	return nil
}

func pathWithinDirectory(path, directory string) (bool, error) {
	path = strings.TrimSpace(path)
	directory = strings.TrimSpace(directory)
	if path == "" {
		return false, errors.New("requester config path is required")
	}
	if directory == "" {
		return false, errors.New("workspace path is required")
	}
	resolvedPath, err := filepath.Abs(path)
	if err != nil {
		return false, fmt.Errorf("resolve requester config path: %w", err)
	}
	resolvedDirectory, err := filepath.Abs(directory)
	if err != nil {
		return false, fmt.Errorf("resolve workspace path: %w", err)
	}
	// Prefer EvalSymlinks when paths exist; fall back to cleaned abs paths.
	if linked, err := filepath.EvalSymlinks(resolvedPath); err == nil {
		resolvedPath = linked
	}
	if linked, err := filepath.EvalSymlinks(resolvedDirectory); err == nil {
		resolvedDirectory = linked
	}
	resolvedPath = filepath.Clean(resolvedPath)
	resolvedDirectory = filepath.Clean(resolvedDirectory)
	rel, err := filepath.Rel(resolvedDirectory, resolvedPath)
	if err != nil {
		return false, err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false, nil
	}
	return true, nil
}

func refreshIntervalFromConfig(cfg Config) time.Duration {
	// <0 disables periodic refresh; 0 means default 10m; >0 is explicit ms.
	if cfg.RequesterConfigRefreshMs < 0 {
		return 0
	}
	if cfg.RequesterConfigRefreshMs == 0 {
		return requesterpolicy.DefaultRefreshInterval
	}
	return time.Duration(cfg.RequesterConfigRefreshMs) * time.Millisecond
}

// OpenRequesterPolicyPreview validates a policy file without starting refresh.
func OpenRequesterPolicyPreview(path, runtimeBotID string) error {
	store, err := requesterpolicy.NewStore(requesterpolicy.Options{
		Path:            path,
		RuntimeBotID:    runtimeBotID,
		RefreshInterval: 0,
	})
	if err != nil {
		return err
	}
	store.Close()
	return nil
}

func openRequesterPolicyStore(cfg Config) (*requesterpolicy.Store, error) {
	path := strings.TrimSpace(cfg.RequesterConfigPath)
	if path == "" {
		return nil, nil
	}
	botID := strings.TrimSpace(cfg.BotID)
	if botID == "" {
		return nil, errors.New("botId is required when requesterConfigPath is set")
	}
	store, err := requesterpolicy.NewStore(requesterpolicy.Options{
		Path:            path,
		RuntimeBotID:    botID,
		RefreshInterval: refreshIntervalFromConfig(cfg),
	})
	if err != nil {
		return nil, err
	}
	info, _ := store.Info()
	log.Printf("wecom requester policy loaded: path=%s revision=%s enabledUsers=%d", info.Path, info.Revision, info.EnabledUserCount)
	return store, nil
}

// resolveRequesterTurn looks up the user and builds context + encrypted host auth.
func resolveRequesterTurn(store *requesterpolicy.Store, userID, requestID, chatID, chatType string) (*requestercontext.Context, requestercontext.HostAuth, bool, error) {
	if store == nil {
		return nil, requestercontext.HostAuth{}, false, nil
	}
	snap := store.Snapshot()
	if snap == nil {
		return nil, requestercontext.HostAuth{}, false, errors.New("requester policy snapshot is empty")
	}
	ctx, user, ok := snap.BuildContext(userID, requestID, chatID, chatType)
	if !ok {
		return nil, requestercontext.HostAuth{}, false, nil
	}
	auth, err := requesterpolicy.EncryptUser(user)
	if err != nil {
		return nil, requestercontext.HostAuth{}, false, err
	}
	return ctx, auth, true, nil
}
