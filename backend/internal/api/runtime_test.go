package api

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pengmide/lumi/internal/config"
)

func TestResolveWorkspaceRuntimeRejectsMissingLocalPath(t *testing.T) {
	server := newTestAPIServer(t)
	missing := filepath.Join(t.TempDir(), "missing")
	server.config.Workspaces = []config.WorkspaceConfig{
		{ID: "missing", Name: "Missing", Path: missing},
	}
	server.config.DefaultWorkspace = "missing"

	_, err := server.resolveWorkspaceRuntime(context.Background(), "missing", nil)
	if err == nil {
		t.Fatal("resolveWorkspaceRuntime() error = nil, want missing path error")
	}
	if !strings.Contains(err.Error(), "workspace path does not exist") {
		t.Fatalf("resolveWorkspaceRuntime() error = %q, want missing path message", err)
	}
}
