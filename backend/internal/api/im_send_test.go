package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pengmide/lumi/internal/config"
	"github.com/pengmide/lumi/internal/wecom"
)

type fakeIMSender struct {
	requests []wecom.IMSendRequest
	err      error
}

func (s *fakeIMSender) SendIM(_ context.Context, req wecom.IMSendRequest) error {
	s.requests = append(s.requests, req)
	return s.err
}

func TestHandleIMSendSendsWeComImageInsideWorkspace(t *testing.T) {
	root := t.TempDir()
	imagePath := filepath.Join(root, "qr.png")
	if err := os.WriteFile(imagePath, []byte("png-data"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	canonicalImagePath, err := filepath.EvalSymlinks(imagePath)
	if err != nil {
		t.Fatalf("EvalSymlinks() error = %v", err)
	}
	sender := &fakeIMSender{}
	server := &Server{
		config:        &config.Config{Workspaces: []config.WorkspaceConfig{{ID: "default", Path: root}}},
		wecomIMSender: sender,
	}

	body := strings.NewReader(`{
		"channel":"wecom",
		"type":"image",
		"path":"qr.png",
		"caption":"scan",
		"workspaceId":"default",
		"wecom":{"reqId":"req-1","chatId":"chat-1","userId":"user-1"}
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/im/send", body)
	rec := httptest.NewRecorder()
	server.handleIMSend(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if len(sender.requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(sender.requests))
	}
	got := sender.requests[0]
	if got.Type != "image" || got.Path != canonicalImagePath || got.Caption != "scan" || got.ReqID != "req-1" {
		t.Fatalf("request = %+v", got)
	}
}

func TestHandleIMSendMapsSandboxWorkspacePathByWorkspaceID(t *testing.T) {
	root := t.TempDir()
	imagePath := filepath.Join(root, "qr.png")
	if err := os.WriteFile(imagePath, []byte("png-data"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	sender := &fakeIMSender{}
	server := &Server{
		config:        &config.Config{Workspaces: []config.WorkspaceConfig{{ID: "sandbox", Path: root, Kind: "sandbox"}}},
		wecomIMSender: sender,
	}

	body := strings.NewReader(`{
		"channel":"wecom",
		"type":"image",
		"path":"/workspace/qr.png",
		"workspaceId":"sandbox",
		"workspacePath":"/workspace",
		"wecom":{"chatId":"chat-1"}
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/im/send", body)
	rec := httptest.NewRecorder()
	server.handleIMSend(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if len(sender.requests) != 1 || sender.requests[0].Path == "/workspace/qr.png" {
		t.Fatalf("requests = %+v, want host workspace path", sender.requests)
	}
}

func TestHandleIMSendRejectsWorkspaceEscape(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.png")
	if err := os.WriteFile(outside, []byte("secret-data"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	server := &Server{
		config:        &config.Config{Workspaces: []config.WorkspaceConfig{{ID: "default", Path: root}}},
		wecomIMSender: &fakeIMSender{},
	}

	body := strings.NewReader(`{
		"channel":"wecom",
		"type":"image",
		"path":"` + strings.ReplaceAll(outside, `\`, `\\`) + `",
		"workspaceId":"default",
		"wecom":{"chatId":"chat-1"}
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/im/send", body)
	rec := httptest.NewRecorder()
	server.handleIMSend(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "path escapes workspace") {
		t.Fatalf("body = %s, want path escapes workspace", rec.Body.String())
	}
}

func TestHandleIMSendSupportsText(t *testing.T) {
	sender := &fakeIMSender{}
	server := &Server{wecomIMSender: sender}
	req := httptest.NewRequest(http.MethodPost, "/api/im/send", strings.NewReader(`{
		"channel":"wecom",
		"type":"text",
		"text":"hello",
		"wecom":{"chatId":"chat-1"}
	}`))
	rec := httptest.NewRecorder()
	server.handleIMSend(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if len(sender.requests) != 1 || sender.requests[0].Type != "text" || sender.requests[0].Text != "hello" {
		t.Fatalf("requests = %+v", sender.requests)
	}
}

func TestHandleIMSendSupportsFile(t *testing.T) {
	root := t.TempDir()
	filePath := filepath.Join(root, "report.txt")
	if err := os.WriteFile(filePath, []byte("report-data"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	sender := &fakeIMSender{}
	server := &Server{
		config:        &config.Config{Workspaces: []config.WorkspaceConfig{{ID: "default", Path: root}}},
		wecomIMSender: sender,
	}
	req := httptest.NewRequest(http.MethodPost, "/api/im/send", strings.NewReader(`{
		"channel":"wecom",
		"type":"file",
		"path":"report.txt",
		"workspaceId":"default",
		"wecom":{"chatId":"chat-1"}
	}`))
	rec := httptest.NewRecorder()
	server.handleIMSend(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if len(sender.requests) != 1 || sender.requests[0].Type != "file" || sender.requests[0].FileName != "report.txt" {
		t.Fatalf("requests = %+v", sender.requests)
	}
}

func TestHandleIMSendRejectsMissingWeComTarget(t *testing.T) {
	server := &Server{wecomIMSender: &fakeIMSender{}}
	req := httptest.NewRequest(http.MethodPost, "/api/im/send", strings.NewReader(`{"channel":"wecom","type":"text","text":"hello"}`))
	rec := httptest.NewRecorder()
	server.handleIMSend(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}
