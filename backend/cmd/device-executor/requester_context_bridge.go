package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/pengmide/lumi/internal/agent"
	"github.com/pengmide/lumi/internal/jsonrpc"
	"github.com/pengmide/lumi/internal/requestercontext"
)

func executorRequesterContextRoot() string {
	if prefix := strings.TrimSpace(os.Getenv("NPM_CONFIG_PREFIX")); prefix != "" {
		return filepath.Join(filepath.Dir(prefix), "requester-context")
	}
	if info, err := os.Stat("/lumi/runtime"); err == nil && info.IsDir() {
		return "/lumi/runtime/requester-context"
	}
	return filepath.Join(os.TempDir(), "lumi-requester-context", strconv.Itoa(os.Getpid()))
}

func (r *Runner) requesterContextBridge(agentID string) (*requestercontext.FileBridge, error) {
	workspaceID := defaultWorkspace
	if r != nil && r.cfg != nil && strings.TrimSpace(r.cfg.WorkspaceID) != "" {
		workspaceID = strings.TrimSpace(r.cfg.WorkspaceID)
	}
	return requestercontext.NewFileBridge(executorRequesterContextRoot(), workspaceID, agentID)
}

func (r *Runner) promptWithRequesterContext(proc *agent.Process, sessionID string, payload TaskExecutePayload) (*jsonrpc.Message, error) {
	params := map[string]any{
		"sessionId": sessionID,
		"prompt": []map[string]string{
			{"type": "text", "text": payload.Prompt},
		},
	}
	if payload.RequesterContext == nil {
		return proc.Request("session/prompt", params)
	}

	bridge, err := r.requesterContextBridge(payload.AgentID)
	if err != nil {
		return nil, err
	}
	path, cleanup, err := bridge.Write(sessionID, *payload.RequesterContext)
	if err != nil {
		return nil, err
	}
	defer func() {
		if cleanupErr := cleanup(); cleanupErr != nil {
			log.Printf("failed to clean requester context file %s: %v", path, cleanupErr)
		}
	}()
	params["_meta"] = requestercontext.PromptMeta(*payload.RequesterContext)
	response, err := proc.Request("session/prompt", params)
	if err != nil {
		return nil, fmt.Errorf("request agent prompt: %w", err)
	}
	return response, nil
}
