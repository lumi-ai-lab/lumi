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

// requesterContextFileEnabled is opt-in. Host auth should travel on ACP `_meta`
// (see Lumi patches for pi-acp@0.0.33 + pi-coding-agent@0.83.0), not Agent-readable files.
func requesterContextFileEnabled() bool {
	v := strings.TrimSpace(os.Getenv("LUMI_REQUESTER_CONTEXT_FILE"))
	switch strings.ToLower(v) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func (r *Runner) requesterContextBridge(agentID string) (*requestercontext.FileBridge, error) {
	workspaceID := defaultWorkspace
	if r != nil && r.cfg != nil && strings.TrimSpace(r.cfg.WorkspaceID) != "" {
		workspaceID = strings.TrimSpace(r.cfg.WorkspaceID)
	}
	return requestercontext.NewFileBridge(executorRequesterContextRoot(), workspaceID, agentID)
}

func (r *Runner) promptWithHostAuth(proc *agent.Process, sessionID string, payload TaskExecutePayload) (*jsonrpc.Message, error) {
	params := map[string]any{
		"sessionId": sessionID,
		"prompt": []map[string]string{
			{"type": "text", "text": payload.Prompt},
		},
	}
	if payload.HostAuth == nil {
		return proc.Request("session/prompt", params)
	}
	// Primary path: ACP prompt `_meta` (pi-acp hostAuth patch → PI context → harness bind).
	params["_meta"] = requestercontext.PromptMeta(*payload.HostAuth, payload.RequesterContext)

	// Optional file envelope (legacy / non-PI). Default OFF so Agent-readable disks
	// do not hold reusable qdm1enc material. Set LUMI_REQUESTER_CONTEXT_FILE=1 to enable.
	var cleanup func() error
	if requesterContextFileEnabled() {
		bridge, err := r.requesterContextBridge(payload.AgentID)
		if err != nil {
			return nil, err
		}
		_, cleanup, err = bridge.Write(sessionID, *payload.HostAuth, payload.RequesterContext)
		if err != nil {
			return nil, err
		}
		defer func() {
			if cleanup == nil {
				return
			}
			if cleanupErr := cleanup(); cleanupErr != nil {
				log.Printf("failed to clean requester context file: %v", cleanupErr)
			}
		}()
	}
	response, err := proc.Request("session/prompt", params)
	if err != nil {
		return nil, fmt.Errorf("request agent prompt: %w", err)
	}
	return response, nil
}
