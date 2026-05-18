package api

import (
	"context"
	"errors"

	"github.com/pengmide/lumi/internal/config"
	"github.com/pengmide/lumi/internal/sandbox"
)

func (s *Server) WarmupSandboxByID(ctx context.Context, workspaceID string) (sandbox.RuntimeState, error) {
	workspace, ok := s.resolveWorkspace(workspaceID)
	if !ok {
		return sandbox.RuntimeState{}, errors.New("workspace not found")
	}
	if !isSandboxWorkspaceConfig(*workspace) {
		return sandbox.RuntimeState{}, errors.New("workspace is not a sandbox")
	}
	return s.WarmupSandbox(ctx, *workspace), nil
}

func (s *Server) EnsureSandboxByID(ctx context.Context, workspaceID string) (sandbox.RuntimeState, error) {
	workspace, ok := s.resolveWorkspace(workspaceID)
	if !ok {
		return sandbox.RuntimeState{}, errors.New("workspace not found")
	}
	if !isSandboxWorkspaceConfig(*workspace) {
		return sandbox.RuntimeState{}, errors.New("workspace is not a sandbox")
	}
	state, runtimeErr := s.sandbox.Ensure(ctx, sandbox.EnsureOptions{
		Workspace:  *workspace,
		BackendURL: s.backendURLForSandbox(nil),
	})
	if runtimeErr != nil {
		return state, runtimeErr
	}
	return state, nil
}

func (s *Server) SandboxStatusByID(workspaceID string) (sandbox.RuntimeState, error) {
	workspace, ok := s.resolveWorkspace(workspaceID)
	if !ok {
		return sandbox.RuntimeState{}, errors.New("workspace not found")
	}
	if !isSandboxWorkspaceConfig(*workspace) {
		return sandbox.RuntimeState{}, errors.New("workspace is not a sandbox")
	}
	return s.sandbox.Status(*workspace), nil
}

func (s *Server) WarmupSandbox(ctx context.Context, workspace config.WorkspaceConfig) sandbox.RuntimeState {
	return s.sandbox.Warmup(ctx, sandbox.EnsureOptions{
		Workspace:  workspace,
		BackendURL: s.backendURLForSandbox(nil),
	})
}
