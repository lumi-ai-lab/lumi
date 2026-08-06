package requesterpolicy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writePolicy(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	return path
}

const samplePolicy = `{
  "version": 2,
  "users": [
    {
      "userId": "pengmingde01",
      "displayName": "彭明德",
      "enabled": true,
      "authorization": {
        "capabilities": ["qdm.metric.query"],
        "claims": {
          "qdm.scope": {
            "schemaVersion": 1,
            "manageAreaIds": ["CN01"],
            "dcManageAreaIds": ["CN01"],
            "categoryLevel1Ids": ["10", "11"]
          }
        }
      }
    },
    {
      "userId": "disabled-user",
      "displayName": "Disabled",
      "enabled": false,
      "authorization": {
        "capabilities": [],
        "claims": {}
      }
    }
  ]
}`

func TestLoadLookupAndSlice(t *testing.T) {
	dir := t.TempDir()
	path := writePolicy(t, dir, "policy.json", samplePolicy)

	store, err := NewStore(Options{Path: path, RuntimeBotID: "bot-1", RefreshInterval: 0})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	info, err := store.Info()
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if !strings.HasPrefix(info.Revision, "sha256:") {
		t.Fatalf("revision = %q", info.Revision)
	}
	if info.EnabledUserCount != 1 {
		t.Fatalf("enabled count = %d", info.EnabledUserCount)
	}

	snap := store.Snapshot()
	if _, ok := snap.Lookup("disabled-user"); ok {
		t.Fatal("disabled user should not resolve")
	}
	if _, ok := snap.Lookup("Pengmingde01"); ok {
		t.Fatal("lookup must be case-sensitive")
	}
	user, ok := snap.Lookup("  pengmingde01  ")
	if !ok {
		t.Fatal("expected enabled user with trim")
	}
	if user.UserID != "pengmingde01" {
		t.Fatalf("userId = %q", user.UserID)
	}

	ctx, _, ok := snap.BuildContext("pengmingde01", "msg-1", "chat-1", "single")
	if !ok || ctx == nil {
		t.Fatal("BuildContext failed")
	}
	if ctx.Principal.BotID != "bot-1" || ctx.Principal.CanonicalUserID != "pengmingde01" {
		t.Fatalf("context principal = %+v", ctx.Principal)
	}

	plain, err := SliceUserDocument(user)
	if err != nil {
		t.Fatalf("SliceUserDocument: %v", err)
	}
	doc, err := DecodeDocument(plain)
	if err != nil {
		t.Fatalf("DecodeDocument sliced: %v", err)
	}
	if len(doc.Users) != 1 || doc.Users[0].UserID != "pengmingde01" {
		t.Fatalf("sliced users = %+v", doc.Users)
	}

	auth, err := EncryptUser(user)
	if err != nil {
		t.Fatalf("EncryptUser: %v", err)
	}
	if !strings.HasPrefix(auth.Auth, BlobPrefix) {
		t.Fatalf("blob prefix: %q", auth.Auth)
	}
	if auth.AuthUserID != "pengmingde01" {
		t.Fatalf("auth user id = %q", auth.AuthUserID)
	}
	key, err := ResolveKey()
	if err != nil {
		t.Fatalf("ResolveKey: %v", err)
	}
	decoded, err := Decrypt(auth.Auth, key)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	roundTrip, err := DecodeDocument(decoded)
	if err != nil {
		t.Fatalf("decode decrypted: %v", err)
	}
	if len(roundTrip.Users) != 1 || roundTrip.Users[0].UserID != "pengmingde01" {
		t.Fatalf("roundTrip = %+v", roundTrip.Users)
	}
}

func TestReloadKeepsSnapshotOnBadFile(t *testing.T) {
	dir := t.TempDir()
	path := writePolicy(t, dir, "policy.json", samplePolicy)
	store, err := NewStore(Options{Path: path, RuntimeBotID: "bot-1", RefreshInterval: 0})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()
	before, _ := store.Info()

	if err := os.WriteFile(path, []byte(`{not-json`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Reload(); err == nil {
		t.Fatal("expected reload error")
	}
	after, err := store.Info()
	if err != nil {
		t.Fatalf("Info after bad reload: %v", err)
	}
	if after.Revision != before.Revision {
		t.Fatalf("snapshot changed on bad reload: %s -> %s", before.Revision, after.Revision)
	}
	if _, ok := store.Snapshot().Lookup("pengmingde01"); !ok {
		t.Fatal("previous user should still resolve")
	}
}

func TestBotIDMismatch(t *testing.T) {
	dir := t.TempDir()
	body := strings.Replace(samplePolicy, `"version": 2,`, `"version": 2, "botId": "other-bot",`, 1)
	path := writePolicy(t, dir, "policy.json", body)
	if _, err := NewStore(Options{Path: path, RuntimeBotID: "bot-1", RefreshInterval: 0}); err == nil {
		t.Fatal("expected botId mismatch error")
	}
}

func TestRejectUnknownFieldsAndBadVersion(t *testing.T) {
	dir := t.TempDir()
	path := writePolicy(t, dir, "bad.json", `{"version":1,"users":[]}`)
	if _, err := NewStore(Options{Path: path, RuntimeBotID: "bot-1", RefreshInterval: 0}); err == nil {
		t.Fatal("expected version error")
	}
	path2 := writePolicy(t, dir, "unknown.json", `{"version":2,"users":[],"extra":true}`)
	if _, err := NewStore(Options{Path: path2, RuntimeBotID: "bot-1", RefreshInterval: 0}); err == nil {
		t.Fatal("expected unknown field error")
	}
}

func TestEncryptNonceUnique(t *testing.T) {
	key, err := ResolveKey()
	if err != nil {
		t.Fatal(err)
	}
	a, err := Encrypt([]byte(`{"version":2,"users":[]}`), key)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Encrypt([]byte(`{"version":2,"users":[]}`), key)
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("expected distinct nonces")
	}
}
