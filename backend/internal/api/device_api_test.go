package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pengmide/lumi/internal/config"
	"github.com/pengmide/lumi/internal/device"
	"github.com/pengmide/lumi/internal/sandbox"
	"github.com/pengmide/lumi/internal/setupcheck"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

func newTestAPIServer(t *testing.T) *Server {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg := &config.Config{
		Agents: []config.AgentConfig{
			{ID: "claude", Name: "Claude Code", Command: "echo"},
			{ID: "codex", Name: "Codex CLI", Command: "echo"},
		},
		DefaultAgent: "claude",
		Workspaces: []config.WorkspaceConfig{
			{ID: "default", Name: "Default", Path: home},
		},
		DefaultWorkspace: "default",
	}

	return NewServer(cfg, nil)
}

func setTestInterfaceAddrs(t *testing.T, values ...string) {
	t.Helper()

	original := listInterfaceAddrs
	listInterfaceAddrs = func() ([]net.Addr, error) {
		addrs := make([]net.Addr, 0, len(values))
		for _, value := range values {
			ip := net.ParseIP(value)
			if ip == nil {
				t.Fatalf("ParseIP(%q) = nil", value)
			}
			if ip.To4() != nil {
				addrs = append(addrs, &net.IPNet{IP: ip, Mask: net.CIDRMask(32, 32)})
				continue
			}
			addrs = append(addrs, &net.IPNet{IP: ip, Mask: net.CIDRMask(128, 128)})
		}
		return addrs, nil
	}
	t.Cleanup(func() {
		listInterfaceAddrs = original
	})
}

func registerTestDevice(t *testing.T, server *Server, deviceID string, ready bool) {
	t.Helper()

	_, err := server.devices.RegisterDevice(device.NewConnection(nil), device.DeviceRegisterPayload{
		DeviceID: deviceID,
		Name:     deviceID,
		Agents:   []device.DeviceAgentInfo{{ID: "claude", Name: "Claude Code"}},
	})
	if err != nil {
		t.Fatalf("RegisterDevice(%s) error = %v", deviceID, err)
	}
	if ready {
		if err := server.devices.UpdateSetupStatus(deviceID, setupcheck.SetupStatus{Ready: true}); err != nil {
			t.Fatalf("UpdateSetupStatus(%s) error = %v", deviceID, err)
		}
	}
}

func TestListDevicesIncludesOfflineAndOnline(t *testing.T) {
	server := newTestAPIServer(t)
	registerTestDevice(t, server, "dev-online", true)
	registerTestDevice(t, server, "dev-offline", true)
	server.devices.MarkDisconnected("dev-offline", "test disconnect")

	req := httptest.NewRequest(http.MethodGet, "/api/devices", nil)
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var response struct {
		Devices []device.DeviceDTO `json:"devices"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if len(response.Devices) != 2 {
		t.Fatalf("len(devices) = %d, want 2", len(response.Devices))
	}

	statuses := map[string]string{}
	for _, dev := range response.Devices {
		statuses[dev.ID] = dev.Status
	}
	if statuses["dev-online"] != device.StatusOnline {
		t.Fatalf("dev-online status = %q, want %q", statuses["dev-online"], device.StatusOnline)
	}
	if statuses["dev-offline"] != device.StatusOffline {
		t.Fatalf("dev-offline status = %q, want %q", statuses["dev-offline"], device.StatusOffline)
	}
}

func TestListDevicesFiltersHiddenSandboxDevices(t *testing.T) {
	server := newTestAPIServer(t)
	_, err := server.devices.RegisterDevice(device.NewConnection(nil), device.DeviceRegisterPayload{
		DeviceID: "sandbox-dev",
		Name:     "sandbox-dev",
		Hidden:   true,
	})
	if err != nil {
		t.Fatalf("RegisterDevice(hidden) error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/devices", nil)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var response struct {
		Devices []device.DeviceDTO `json:"devices"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if len(response.Devices) != 0 {
		t.Fatalf("len(devices) = %d, want 0", len(response.Devices))
	}
}

func TestDeleteOfflineDevice(t *testing.T) {
	server := newTestAPIServer(t)
	registerTestDevice(t, server, "dev-offline", true)
	server.devices.MarkDisconnected("dev-offline", "test disconnect")

	req := httptest.NewRequest(http.MethodDelete, "/api/devices/dev-offline", nil)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if _, ok := server.devices.GetDevice("dev-offline"); ok {
		t.Fatalf("device still present after delete")
	}
}

func TestRequestSetupCheckRejectsOfflineDevice(t *testing.T) {
	server := newTestAPIServer(t)
	registerTestDevice(t, server, "dev-offline", false)
	server.devices.MarkDisconnected("dev-offline", "test disconnect")

	req := httptest.NewRequest(http.MethodPost, "/api/devices/dev-offline/setup/check", nil)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusConflict)
	}
}

func TestDeviceResetClearsRemoteSessions(t *testing.T) {
	server := newTestAPIServer(t)
	registerPayload := device.DeviceRegisterPayload{
		DeviceID: "dev-1",
		Name:     "dev-1",
		Agents:   []device.DeviceAgentInfo{{ID: "claude", Name: "Claude Code"}},
	}

	if _, err := server.devices.RegisterDevice(device.NewConnection(nil), registerPayload); err != nil {
		t.Fatalf("RegisterDevice(conn1) error = %v", err)
	}
	server.setRemoteSession("conv-1", "dev-1", "claude", "remote-session-1")

	if _, err := server.devices.RegisterDevice(device.NewConnection(nil), registerPayload); err != nil {
		t.Fatalf("RegisterDevice(conn2) error = %v", err)
	}
	if got := server.getRemoteSession("conv-1", "dev-1", "claude"); got != "" {
		t.Fatalf("getRemoteSession() after reconnect = %q, want empty", got)
	}

	server.setRemoteSession("conv-1", "dev-1", "claude", "remote-session-2")
	server.devices.MarkDisconnected("dev-1", "test disconnect")
	if got := server.getRemoteSession("conv-1", "dev-1", "claude"); got != "" {
		t.Fatalf("getRemoteSession() after disconnect = %q, want empty", got)
	}
}

func TestWebSocketRouteIsRegistered(t *testing.T) {
	server := newTestAPIServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/devices/ws", nil)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestPairingCommandUsesConfiguredPublicServerURL(t *testing.T) {
	server := newTestAPIServer(t)
	server.config.PublicServerURL = "https://chat.example.com/lumi"
	setTestInterfaceAddrs(t, "10.0.0.25")

	req := httptest.NewRequest(http.MethodGet, "/api/devices/pairing-command", nil)
	req.Host = "localhost:3000"
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var response struct {
		Command string `json:"command"`
		Server  string `json:"server"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if response.Server != "https://chat.example.com/lumi" {
		t.Fatalf("server = %q, want configured public URL", response.Server)
	}
	if !strings.HasPrefix(response.Command, "device-executor connect --server https://chat.example.com/lumi --token ") {
		t.Fatalf("command = %q, want configured pairing command prefix", response.Command)
	}
}

func TestPairingCommandUsesPrivateIPv4WhenConfigMissing(t *testing.T) {
	server := newTestAPIServer(t)
	setTestInterfaceAddrs(t, "127.0.0.1", "169.254.10.20", "10.0.0.25", "203.0.113.9")

	req := httptest.NewRequest(http.MethodGet, "/api/devices/pairing-command", nil)
	req.Host = "localhost:4173"
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	var response struct {
		Server string `json:"server"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if response.Server != "http://10.0.0.25:4173" {
		t.Fatalf("server = %q, want private IPv4 pairing address", response.Server)
	}
}

func TestPairingCommandIgnoresInvalidConfiguredURL(t *testing.T) {
	server := newTestAPIServer(t)
	server.config.PublicServerURL = "localhost:3000"
	setTestInterfaceAddrs(t, "192.168.1.23")

	req := httptest.NewRequest(http.MethodGet, "/api/devices/pairing-command", nil)
	req.Host = "localhost:3000"
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	var response struct {
		Server string `json:"server"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if response.Server != "http://192.168.1.23:3000" {
		t.Fatalf("server = %q, want automatic IP fallback", response.Server)
	}
}

func TestPairingCommandFallsBackToFirstNonLoopbackIPv4(t *testing.T) {
	server := newTestAPIServer(t)
	setTestInterfaceAddrs(t, "127.0.0.1", "203.0.113.10", "198.51.100.2")

	req := httptest.NewRequest(http.MethodGet, "/api/devices/pairing-command", nil)
	req.Host = "localhost:3000"
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	var response struct {
		Server string `json:"server"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if response.Server != "http://203.0.113.10:3000" {
		t.Fatalf("server = %q, want first non-loopback IPv4", response.Server)
	}
}

func TestPairingCommandFallsBackToLocalhostWhenNoUsableIPv4(t *testing.T) {
	server := newTestAPIServer(t)
	setTestInterfaceAddrs(t, "127.0.0.1", "::1", "fe80::1", "169.254.10.20")

	req := httptest.NewRequest(http.MethodGet, "/api/devices/pairing-command", nil)
	req.Host = ""
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	var response struct {
		Server string `json:"server"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if response.Server != "http://localhost:3000" {
		t.Fatalf("server = %q, want localhost fallback", response.Server)
	}
}

func TestRemoteWorkspaceListIncludesDynamicDeviceStatus(t *testing.T) {
	server := newTestAPIServer(t)
	registerTestDevice(t, server, "dev-1", true)
	server.config.Workspaces = append(server.config.Workspaces, config.WorkspaceConfig{
		ID:         "remote-ws",
		Name:       "Remote",
		Path:       filepath.Join(t.TempDir(), "remote"),
		Kind:       "remote",
		DeviceID:   "dev-1",
		DeviceName: "Office Mac",
		RemotePath: "/Users/me/project",
	})

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces", nil)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var response struct {
		Workspaces []map[string]any `json:"workspaces"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	var remote map[string]any
	for _, workspace := range response.Workspaces {
		if workspace["id"] == "remote-ws" {
			remote = workspace
			break
		}
	}
	if remote == nil {
		t.Fatalf("remote workspace not found in response")
	}
	if remote["deviceStatus"] != device.StatusOnline {
		t.Fatalf("deviceStatus = %v, want %q", remote["deviceStatus"], device.StatusOnline)
	}
	if remote["setupReady"] != true {
		t.Fatalf("setupReady = %v, want true", remote["setupReady"])
	}
}

func TestSandboxWorkspaceListIncludesSandboxState(t *testing.T) {
	server := newTestAPIServer(t)
	sandboxDir := t.TempDir()
	server.config.Workspaces = append(server.config.Workspaces, config.WorkspaceConfig{
		ID:             "sandbox-ws",
		Name:           "Sandbox",
		Path:           sandboxDir,
		Kind:           "sandbox",
		Image:          "lumi/sandbox:latest",
		IdleTimeoutSec: 1800,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces", nil)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var response struct {
		Workspaces []map[string]any `json:"workspaces"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	var sandboxWS map[string]any
	for _, workspace := range response.Workspaces {
		if workspace["id"] == "sandbox-ws" {
			sandboxWS = workspace
			break
		}
	}
	if sandboxWS == nil {
		t.Fatalf("sandbox workspace not found in response")
	}
	if sandboxWS["sandboxStatus"] != "terminated" {
		t.Fatalf("sandboxStatus = %v, want terminated", sandboxWS["sandboxStatus"])
	}
	if sandboxWS["sandboxReady"] != false {
		t.Fatalf("sandboxReady = %v, want false", sandboxWS["sandboxReady"])
	}
}

func TestCreateSandboxWorkspaceStoresDefaults(t *testing.T) {
	server := newTestAPIServer(t)
	workspaceDir := t.TempDir()

	body := bytes.NewBufferString(`{"name":"Sandbox","path":"` + filepath.ToSlash(workspaceDir) + `","kind":"sandbox","agents":["claude"]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var response struct {
		Workspace config.WorkspaceConfig `json:"workspace"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if response.Workspace.Kind != "sandbox" {
		t.Fatalf("kind = %q, want sandbox", response.Workspace.Kind)
	}
	if response.Workspace.Image != "lumi/sandbox:latest" {
		t.Fatalf("image = %q, want lumi/sandbox:latest", response.Workspace.Image)
	}
	if response.Workspace.IdleTimeoutSec != 1800 {
		t.Fatalf("idleTimeoutSec = %d, want 1800", response.Workspace.IdleTimeoutSec)
	}
	if len(response.Workspace.Agents) != 1 || response.Workspace.Agents[0] != "claude" {
		t.Fatalf("agents = %#v, want [claude]", response.Workspace.Agents)
	}
}

func TestSandboxPreflightRejectsInvalidPath(t *testing.T) {
	server := newTestAPIServer(t)

	body := bytes.NewBufferString(`{"path":"relative/path"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/sandbox/preflight", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var response struct {
		OK   bool   `json:"ok"`
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if response.OK {
		t.Fatalf("ok = true, want false")
	}
	if response.Code != "path_invalid" {
		t.Fatalf("code = %q, want path_invalid", response.Code)
	}
}

func TestWorkspaceListDefaultSkipsSandboxWorkspace(t *testing.T) {
	server := newTestAPIServer(t)
	server.config.Workspaces = []config.WorkspaceConfig{
		{
			ID:   "sandbox-ws",
			Name: "Sandbox",
			Path: t.TempDir(),
			Kind: "sandbox",
		},
		{
			ID:   "local-ws",
			Name: "Local",
			Path: t.TempDir(),
			Kind: "local",
		},
	}
	server.config.DefaultWorkspace = "sandbox-ws"

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces", nil)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var response struct {
		Default string `json:"default"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if response.Default != "local-ws" {
		t.Fatalf("default = %q, want local-ws", response.Default)
	}

	ws, ok := server.resolveWorkspace("")
	if !ok || ws.ID != "local-ws" {
		t.Fatalf("resolveWorkspace(\"\") = %#v, %v; want local-ws", ws, ok)
	}
}

func TestWorkspaceResolverDoesNotImplicitlySelectOnlySandboxWorkspace(t *testing.T) {
	server := newTestAPIServer(t)
	server.config.Workspaces = []config.WorkspaceConfig{
		{
			ID:   "sandbox-ws",
			Name: "Sandbox",
			Path: t.TempDir(),
			Kind: "sandbox",
		},
	}
	server.config.DefaultWorkspace = "sandbox-ws"

	if defaultID := server.defaultWorkspaceID(); defaultID != "" {
		t.Fatalf("defaultWorkspaceID() = %q, want empty", defaultID)
	}
	if ws, ok := server.resolveWorkspace(""); ok || ws != nil {
		t.Fatalf("resolveWorkspace(\"\") = %#v, %v; want no implicit workspace", ws, ok)
	}
}

func TestNewSessionUsesNonSandboxDefaultWorkspace(t *testing.T) {
	server := newTestAPIServer(t)
	server.config.Workspaces = []config.WorkspaceConfig{
		{
			ID:   "sandbox-ws",
			Name: "Sandbox",
			Path: t.TempDir(),
			Kind: "sandbox",
		},
		{
			ID:   "local-ws",
			Name: "Local",
			Path: t.TempDir(),
			Kind: "local",
		},
	}
	server.config.DefaultWorkspace = "sandbox-ws"

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/new", bytes.NewBufferString(`{}`))
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var response struct {
		Session struct {
			WorkspaceID string `json:"workspaceId"`
		} `json:"session"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if response.Session.WorkspaceID != "local-ws" {
		t.Fatalf("workspaceId = %q, want local-ws", response.Session.WorkspaceID)
	}
}

func TestSandboxRuntimeErrorWritesStableCode(t *testing.T) {
	rec := httptest.NewRecorder()
	writeSandboxRuntimeError(rec, &sandbox.RuntimeError{
		Code:    sandbox.CodeDockerUnavailable,
		Message: "Cannot connect to Docker",
		Details: "unix:///var/run/docker.sock",
	})

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}

	var response struct {
		Error   string `json:"error"`
		Message string `json:"message"`
		Details string `json:"details"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if response.Error != sandbox.CodeDockerUnavailable {
		t.Fatalf("error = %q, want %q", response.Error, sandbox.CodeDockerUnavailable)
	}
	if response.Message != "Cannot connect to Docker" {
		t.Fatalf("message = %q, want Cannot connect to Docker", response.Message)
	}
	if response.Details == "" {
		t.Fatalf("details is empty, want debug details")
	}
}

func TestSandboxWorkspaceAccessErrorWritesStableCode(t *testing.T) {
	rec := httptest.NewRecorder()
	writeRuntimeWorkspaceError(rec, ResolvedRuntime{Mode: "sandbox"}, device.ErrDeviceOffline)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}

	var response struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if response.Error != sandbox.CodeSandboxUnavailable {
		t.Fatalf("error = %q, want %q", response.Error, sandbox.CodeSandboxUnavailable)
	}
}

func TestSandboxInternalRoutesRejectNonLocalRequests(t *testing.T) {
	server := newTestAPIServer(t)

	req := httptest.NewRequest(http.MethodGet, "/sandboxes/default", nil)
	req.RemoteAddr = "203.0.113.10:49999"
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestSandboxInternalRoutesAllowLocalStatusRequests(t *testing.T) {
	server := newTestAPIServer(t)

	req := httptest.NewRequest(http.MethodGet, "/sandboxes/default", nil)
	req.RemoteAddr = "127.0.0.1:49999"
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

func TestRemoteWorkspaceTreeUsesConnectedDevice(t *testing.T) {
	server := newTestAPIServer(t)
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	secret, err := device.EnsureSecret("")
	if err != nil {
		t.Fatalf("EnsureSecret() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn := connectTestDevice(t, ctx, httpServer.URL, secret)
	defer conn.Close(websocket.StatusNormalClosure, "done")
	registerAndReadyDevice(t, ctx, conn)

	remoteDir := t.TempDir()
	server.config.Workspaces = append(server.config.Workspaces, config.WorkspaceConfig{
		ID:         "remote-ws",
		Name:       "Remote",
		Path:       remoteDir,
		Kind:       "remote",
		DeviceID:   "dev-1",
		DeviceName: "Office Mac",
		RemotePath: remoteDir,
	})

	treeDone := make(chan struct{})
	go func() {
		env := readEnvelope(t, ctx, conn)
		if env.Type != device.MsgWorkspaceTree {
			t.Errorf("workspace request type = %q, want %q", env.Type, device.MsgWorkspaceTree)
			close(treeDone)
			return
		}
		var payload device.WorkspaceRequestPayload
		if err := json.Unmarshal(env.Payload, &payload); err != nil {
			t.Errorf("json.Unmarshal(workspace request payload) error = %v", err)
			close(treeDone)
			return
		}
		if payload.Path != "docs" {
			t.Errorf("workspace request path = %q, want docs", payload.Path)
		}
		resp, err := device.NewEnvelope(device.MsgWorkspaceResponse, env.ID, "dev-1", "", device.WorkspaceResponsePayload{
			OK:      true,
			Payload: json.RawMessage(`{"tree":[{"path":"docs/README.md","name":"README.md","isDir":false,"previewKind":"markdown"}]}`),
		})
		if err != nil {
			t.Errorf("NewEnvelope(workspace.response) error = %v", err)
			close(treeDone)
			return
		}
		if err := wsjson.Write(ctx, conn, resp); err != nil {
			t.Errorf("wsjson.Write(workspace.response) error = %v", err)
		}
		close(treeDone)
	}()

	resp, err := http.Get(httpServer.URL + "/api/workspaces/tree?workspaceId=remote-ws&path=docs")
	if err != nil {
		t.Fatalf("GET tree error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var response struct {
		Tree []map[string]any `json:"tree"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if len(response.Tree) != 1 || response.Tree[0]["path"] != "docs/README.md" {
		t.Fatalf("tree = %+v, want docs/README.md", response.Tree)
	}
	<-treeDone
}

func TestLocalWorkspaceTreeStillReadsBackendFilesystem(t *testing.T) {
	server := newTestAPIServer(t)
	workspaceDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspaceDir, "local.txt"), []byte("local"), 0644); err != nil {
		t.Fatalf("WriteFile(local.txt) error = %v", err)
	}
	server.config.Workspaces = []config.WorkspaceConfig{
		{ID: "local-ws", Name: "Local", Path: workspaceDir},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/tree?workspaceId=local-ws", nil)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var response struct {
		Tree []map[string]any `json:"tree"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if len(response.Tree) != 1 || response.Tree[0]["path"] != "local.txt" {
		t.Fatalf("tree = %+v, want local.txt", response.Tree)
	}
}

func TestLocalWorkspaceTreeReadsRequestedDirectoryOnly(t *testing.T) {
	server := newTestAPIServer(t)
	workspaceDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspaceDir, "docs"), 0755); err != nil {
		t.Fatalf("MkdirAll(docs) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspaceDir, "docs", "readme.md"), []byte("# docs"), 0644); err != nil {
		t.Fatalf("WriteFile(readme.md) error = %v", err)
	}
	server.config.Workspaces = []config.WorkspaceConfig{
		{ID: "local-ws", Name: "Local", Path: workspaceDir},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/tree?workspaceId=local-ws&path=docs", nil)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var response struct {
		Tree []map[string]any `json:"tree"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if len(response.Tree) != 1 || response.Tree[0]["path"] != "docs/readme.md" {
		t.Fatalf("tree = %+v, want docs/readme.md", response.Tree)
	}
}
