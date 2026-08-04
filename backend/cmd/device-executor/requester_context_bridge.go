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
	"github.com/pengmide/lumi/internal/sessioninstruction"
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
	defaultRoot := executorRequesterContextRoot()
	return requestercontext.NewFileBridge(defaultRoot, workspaceID, agentID)
}

func (r *Runner) promptWithRequesterContext(proc *agent.Process, sessionID string, payload TaskExecutePayload) (*jsonrpc.Message, error) {
	promptText := payload.Prompt
	params := map[string]any{
		"sessionId": sessionID,
		"prompt": []map[string]string{
			{"type": "text", "text": promptText},
		},
	}
	if payload.RequesterContext != nil {
		params["_meta"] = requestercontext.PromptMeta(*payload.RequesterContext)
	}
	if payload.InstructionProfile != nil {
		support := proc.SessionInstructionSupport()
		if err := sessioninstruction.ApplyProfile(params, support, *payload.InstructionProfile, sessioninstruction.PhasePrompt); err != nil {
			return nil, err
		}
		if payload.TurnContext != "" && !sessioninstruction.ApplyTurnContext(params, support, payload.TurnContext) {
			promptText = sessioninstruction.WithUntrustedTurnContext(promptText, payload.TurnContext)
			params["prompt"] = []map[string]string{{"type": "text", "text": promptText}}
		}
	}
	if payload.RequesterContext == nil {
		return proc.Request("session/prompt", params)
	}

	bridge, err := r.requesterContextBridge(payload.AgentID)
	if err != nil {
		return nil, err
	}
	_, cleanup, err := bridge.Write(sessionID, *payload.RequesterContext)
	if err != nil {
		return nil, err
	}
	defer func() {
		if cleanupErr := cleanup(); cleanupErr != nil {
			log.Printf("failed to clean requester context file: %v", cleanupErr)
		}
	}()
	response, err := proc.Request("session/prompt", params)
	if err != nil {
		return nil, fmt.Errorf("request agent prompt: %w", err)
	}
	return response, nil
}
