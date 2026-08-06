package wecom

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateRequesterConfigOutsideWorkspace(t *testing.T) {
	root := t.TempDir()
	ws := filepath.Join(root, "workspace")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "policy.json")
	inside := filepath.Join(ws, "policy.json")
	if err := os.WriteFile(outside, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inside, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRequesterConfigOutsideWorkspace(outside, ws); err != nil {
		t.Fatalf("outside should pass: %v", err)
	}
	if err := ValidateRequesterConfigOutsideWorkspace(inside, ws); err == nil {
		t.Fatal("inside should fail")
	}
}

func TestOpenRequesterPolicyPreviewAndResolve(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policy.json")
	body := `{
  "version": 2,
  "users": [{
    "userId": "pengmingde01",
    "displayName": "彭明德",
    "enabled": true,
    "authorization": {
      "capabilities": ["qdm.metric.query"],
      "claims": {"qdm.scope": {"schemaVersion": 1, "manageAreaIds": ["CN01"], "categoryLevel1Ids": ["10"]}}
    }
  }]
}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := OpenRequesterPolicyPreview(path, "bot-1"); err != nil {
		t.Fatalf("preview: %v", err)
	}
	store, err := openRequesterPolicyStore(Config{
		BotID:                    "bot-1",
		RequesterConfigPath:      path,
		RequesterConfigRefreshMs: -1,
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	ctx, auth, ok, err := resolveRequesterTurn(store, "pengmingde01", "msg-1", "chat-1", "single")
	if err != nil || !ok {
		t.Fatalf("resolve: ok=%v err=%v", ok, err)
	}
	if ctx.Principal.CanonicalUserID != "pengmingde01" {
		t.Fatalf("principal = %+v", ctx.Principal)
	}
	if !strings.HasPrefix(auth.Auth, "qdm1enc.") || auth.AuthUserID != "pengmingde01" {
		t.Fatalf("host auth = %+v", auth)
	}
	_, _, ok, err = resolveRequesterTurn(store, "unknown", "msg-2", "chat-1", "single")
	if err != nil || ok {
		t.Fatalf("unknown should deny: ok=%v err=%v", ok, err)
	}
}

func TestRefreshIntervalFromConfig(t *testing.T) {
	if d := refreshIntervalFromConfig(Config{RequesterConfigRefreshMs: 0}); d.Minutes() != 10 {
		t.Fatalf("default interval = %v", d)
	}
	if d := refreshIntervalFromConfig(Config{RequesterConfigRefreshMs: -1}); d != 0 {
		t.Fatalf("disabled interval = %v", d)
	}
	if d := refreshIntervalFromConfig(Config{RequesterConfigRefreshMs: 30000}); d.Seconds() != 30 {
		t.Fatalf("custom interval = %v", d)
	}
}
