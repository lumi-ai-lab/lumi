package api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/pengmide/lumi/internal/config"
	"github.com/pengmide/lumi/internal/sandbox"
	"github.com/pengmide/lumi/web"
)

func TestResolveStaticPathPrefersFileOverDirectory(t *testing.T) {
	t.Parallel()

	staticFS := web.MustFS()
	got := resolveStaticPath("c", staticFS)
	if got != "c.html" {
		t.Fatalf("resolveStaticPath(\"c\") = %q, want %q", got, "c.html")
	}
}

func TestShutdownPreservesSandboxContainers(t *testing.T) {
	fakeSandbox := &fakeSandboxManager{hasActiveRuntime: true}
	server := &Server{sandbox: fakeSandbox}

	output := captureStderr(t, func() {
		if err := server.Shutdown(); err != nil {
			t.Fatalf("Shutdown() error = %v", err)
		}
	})
	if fakeSandbox.shutdownPreserveCalls != 1 {
		t.Fatalf("ShutdownPreserveContainers calls = %d, want 1", fakeSandbox.shutdownPreserveCalls)
	}
	if fakeSandbox.terminateCalls != 0 {
		t.Fatalf("Terminate calls = %d, want 0", fakeSandbox.terminateCalls)
	}
	for _, want := range []string{
		"\n⏳ Shutdown\n",
		strings.Repeat("─", outputRuleWidth),
		"   Stopping WeChat service...\n",
		"   Stopping sandbox manager (containers preserved)...\n",
		"   Shutdown complete.\n",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("shutdown output missing %q in:\n%s", want, output)
		}
	}
}

func TestShutdownClosesSandboxManagerWithoutActiveRuntimeSilently(t *testing.T) {
	fakeSandbox := &fakeSandboxManager{}
	server := &Server{sandbox: fakeSandbox}

	output := captureStderr(t, func() {
		if err := server.Shutdown(); err != nil {
			t.Fatalf("Shutdown() error = %v", err)
		}
	})
	if fakeSandbox.shutdownPreserveCalls != 1 {
		t.Fatalf("ShutdownPreserveContainers calls = %d, want 1", fakeSandbox.shutdownPreserveCalls)
	}
	if strings.Contains(output, "Stopping sandbox manager (containers preserved)...") {
		t.Fatalf("shutdown output should not mention inactive sandbox manager:\n%s", output)
	}
	if !strings.Contains(output, "   Shutdown complete.\n") {
		t.Fatalf("shutdown output missing completion:\n%s", output)
	}
}

func TestShutdownLogsFailedStep(t *testing.T) {
	wantErr := errors.New("sandbox close failed")
	fakeSandbox := &fakeSandboxManager{shutdownErr: wantErr}
	server := &Server{sandbox: fakeSandbox}

	output := captureStderr(t, func() {
		if err := server.Shutdown(); !errors.Is(err, wantErr) {
			t.Fatalf("Shutdown() error = %v, want %v", err, wantErr)
		}
	})

	want := "   Stopping sandbox manager (containers preserved)... failed: sandbox close failed\n"
	if !strings.Contains(output, want) {
		t.Fatalf("shutdown output missing %q in:\n%s", want, output)
	}
}

func TestBackendURLForSandboxUsesConfiguredPublicServerURLWithoutRequest(t *testing.T) {
	server := &Server{config: &config.Config{PublicServerURL: "http://127.0.0.1:39231/"}}

	if got := server.backendURLForSandbox(nil); got != "http://127.0.0.1:39231" {
		t.Fatalf("backendURLForSandbox(nil) = %q, want configured URL", got)
	}
}

func TestHandleSandboxWarmupStartsWarmup(t *testing.T) {
	fakeSandbox := &fakeSandboxManager{
		warmupState: sandbox.RuntimeState{
			WorkspaceID: "sandbox-1",
			Status:      sandbox.StatusPending,
			Stage:       sandbox.StageCheckingDocker,
		},
	}
	server := &Server{
		config: &config.Config{Workspaces: []config.WorkspaceConfig{{
			ID:   "sandbox-1",
			Name: "Sandbox",
			Path: t.TempDir(),
			Kind: "sandbox",
		}}},
		sandbox: fakeSandbox,
	}

	body := bytes.NewBufferString(`{"workspaceId":"sandbox-1"}`)
	req := httptest.NewRequest(http.MethodPost, "/sandboxes/warmup", body)
	req.RemoteAddr = "127.0.0.1:12345"
	rec := httptest.NewRecorder()
	server.handleSandboxes(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if fakeSandbox.warmupCalls != 1 {
		t.Fatalf("warmup calls = %d, want 1", fakeSandbox.warmupCalls)
	}
	if !strings.Contains(rec.Body.String(), `"stage":"checking_docker"`) {
		t.Fatalf("body = %s, want checking_docker stage", rec.Body.String())
	}
}

type fakeSandboxManager struct {
	shutdownPreserveCalls int
	terminateCalls        int
	shutdownErr           error
	ensureState           sandbox.RuntimeState
	ensureErr             *sandbox.RuntimeError
	ensureCalls           int
	warmupState           sandbox.RuntimeState
	warmupCalls           int
	hasActiveRuntime      bool
}

func (f *fakeSandboxManager) Ensure(context.Context, sandbox.EnsureOptions) (sandbox.RuntimeState, *sandbox.RuntimeError) {
	f.ensureCalls++
	return f.ensureState, f.ensureErr
}

func (f *fakeSandboxManager) HasActiveRuntime() bool {
	return f.hasActiveRuntime
}

func (f *fakeSandboxManager) KeepAlive(config.WorkspaceConfig) {}

func (f *fakeSandboxManager) Preflight(context.Context, sandbox.PreflightRequest) sandbox.PreflightResponse {
	return sandbox.PreflightResponse{}
}

func (f *fakeSandboxManager) ShutdownPreserveContainers() error {
	f.shutdownPreserveCalls++
	return f.shutdownErr
}

func (f *fakeSandboxManager) Status(config.WorkspaceConfig) sandbox.RuntimeState {
	return sandbox.RuntimeState{}
}

func (f *fakeSandboxManager) Terminate(context.Context, string) error {
	f.terminateCalls++
	return nil
}

func (f *fakeSandboxManager) Warmup(context.Context, sandbox.EnsureOptions) sandbox.RuntimeState {
	f.warmupCalls++
	return f.warmupState
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()

	original := os.Stderr
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}
	os.Stderr = writer
	t.Cleanup(func() {
		os.Stderr = original
	})

	fn()

	if err := writer.Close(); err != nil {
		t.Fatalf("close stderr writer error = %v", err)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read stderr error = %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("close stderr reader error = %v", err)
	}
	os.Stderr = original
	return string(data)
}
