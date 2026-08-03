package requestercontext_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/pengmide/lumi/internal/wecom"
)

func TestDemoPolicyLoadsAndBuildsContext(t *testing.T) {
	policy, err := wecom.LoadRequesterPolicy(filepath.Join("testdata", "wecom-requesters.json"), "bot-demo-001")
	if err != nil {
		t.Fatalf("LoadRequesterPolicy() error = %v", err)
	}
	if policy.EnabledUserCount() != 1 {
		t.Fatalf("EnabledUserCount() = %d, want 1", policy.EnabledUserCount())
	}

	ctx, ok := policy.BuildContext("user-demo-allowed", "message-demo-001", "chat-demo-001", "group")
	if !ok {
		t.Fatal("BuildContext(allowed user) ok = false")
	}
	if ctx.Version != 2 || ctx.Principal.Channel != "wecom" || ctx.Principal.BotID != "bot-demo-001" || ctx.Principal.CanonicalUserID != "user-demo-allowed" {
		t.Fatalf("requester context identity = %#v", ctx)
	}
	if len(ctx.Authorization.Capabilities) != 2 || ctx.Authorization.Capabilities[0] != "qdm.cmr.query" {
		t.Fatalf("capabilities = %#v", ctx.Authorization.Capabilities)
	}
	var claim struct {
		SchemaVersion     int      `json:"schemaVersion"`
		ManageAreaIDs     []string `json:"manageAreaIds"`
		DCManageAreaIDs   []string `json:"dcManageAreaIds"`
		CategoryLevel1IDs []string `json:"categoryLevel1Ids"`
	}
	if err := json.Unmarshal(ctx.Authorization.Claims["qdm.scope"], &claim); err != nil {
		t.Fatalf("json.Unmarshal(qdm.scope) error = %v", err)
	}
	if claim.SchemaVersion != 1 || claim.ManageAreaIDs[0] != "area-demo" || claim.DCManageAreaIDs[0] != "dc-area-demo" || claim.CategoryLevel1IDs[0] != "category-demo" {
		t.Fatalf("qdm.scope = %#v", claim)
	}
	if _, ok := policy.BuildContext("user-demo-disabled", "", "", ""); ok {
		t.Fatal("BuildContext(disabled user) ok = true")
	}
}

func TestExternalMigratedPolicyLoadsWhenConfigured(t *testing.T) {
	path := os.Getenv("LUMI_E2E_REQUESTER_POLICY")
	if path == "" {
		t.Skip("LUMI_E2E_REQUESTER_POLICY is not configured")
	}
	policy, err := wecom.LoadRequesterPolicy(path, "policy-validation-bot")
	if err != nil {
		t.Fatalf("LoadRequesterPolicy(%s) error = %v", path, err)
	}
	if policy.EnabledUserCount() == 0 {
		t.Fatal("external policy has no enabled users")
	}
}
