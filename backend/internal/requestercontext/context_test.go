package requestercontext

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNormalizeAuthorization(t *testing.T) {
	auth, err := NormalizeAuthorization(Authorization{
		Capabilities: []string{"qdm.metric.query"},
		Claims: Claims{
			"qdm.scope": json.RawMessage(`{"schemaVersion":1,"manageAreaIds":["CN01"]}`),
		},
	})
	if err != nil {
		t.Fatalf("NormalizeAuthorization: %v", err)
	}
	if len(auth.Capabilities) != 1 {
		t.Fatalf("capabilities = %v", auth.Capabilities)
	}
	if _, err := NormalizeAuthorization(Authorization{Capabilities: []string{"Bad"}}); err == nil {
		t.Fatal("expected invalid capability")
	}
	if _, err := NormalizeAuthorization(Authorization{
		Capabilities: []string{"qdm.metric.query", "qdm.metric.query"},
	}); err == nil {
		t.Fatal("expected duplicate capability")
	}
}

func TestPromptMetaKeys(t *testing.T) {
	meta := PromptMeta(HostAuth{Auth: "qdm1enc.abc", AuthUserID: "pengmingde01"}, &Context{
		Version: CurrentContextVersion,
		Principal: Principal{
			Channel:         "wecom",
			BotID:           "bot",
			CanonicalUserID: "pengmingde01",
		},
	})
	raw, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["_auth"] != "qdm1enc.abc" {
		t.Fatalf("_auth = %v", decoded["_auth"])
	}
	if decoded["_auth_user_id"] != "pengmingde01" {
		t.Fatalf("_auth_user_id = %v", decoded["_auth_user_id"])
	}
	if _, ok := decoded["authBlob"]; ok {
		t.Fatal("must not use authBlob alias")
	}
	lumi, ok := decoded["lumi"].(map[string]any)
	if !ok {
		t.Fatalf("lumi missing: %v", decoded)
	}
	if _, ok := lumi["requesterContext"]; !ok {
		t.Fatal("requesterContext missing")
	}
}

func TestFileBridgeWriteCleanup(t *testing.T) {
	root := t.TempDir()
	fixed := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	bridge, err := NewFileBridge(root, "ws1", "agent1", WithClock(func() time.Time { return fixed }), WithTTL(time.Minute))
	if err != nil {
		t.Fatalf("NewFileBridge: %v", err)
	}
	auth := HostAuth{Auth: "qdm1enc.test", AuthUserID: "u1"}
	ctx := &Context{Version: CurrentContextVersion, RequestID: "r1"}
	path, cleanup, err := bridge.Write("session-1", auth, ctx)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatal(err)
	}
	if env.Auth != "qdm1enc.test" || env.AuthUserID != "u1" {
		t.Fatalf("envelope auth = %+v", env)
	}
	if !env.ExpiresAt.Equal(fixed.Add(time.Minute)) {
		t.Fatalf("expiresAt = %v", env.ExpiresAt)
	}
	if !strings.Contains(string(data), `"_auth"`) || !strings.Contains(string(data), `"_auth_user_id"`) {
		t.Fatalf("json keys missing: %s", data)
	}

	// Newer write then stale cleanup must not delete new file.
	path2, cleanup2, err := bridge.Write("session-1", HostAuth{Auth: "qdm1enc.newer", AuthUserID: "u1"}, nil)
	if err != nil {
		t.Fatalf("Write2: %v", err)
	}
	if path2 != path {
		t.Fatalf("paths differ: %s vs %s", path, path2)
	}
	if err := cleanup(); err != nil {
		t.Fatalf("stale cleanup: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("new file removed by stale cleanup: %v", err)
	}
	if err := cleanup2(); err != nil {
		t.Fatalf("cleanup2: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected file removed, err=%v", err)
	}
}

func TestSessionFileNameStable(t *testing.T) {
	a, err := SessionFileName("sess")
	if err != nil {
		t.Fatal(err)
	}
	b, err := SessionFileName("sess")
	if err != nil {
		t.Fatal(err)
	}
	if a != b || filepath.Ext(a) != ".json" {
		t.Fatalf("names = %q %q", a, b)
	}
}
