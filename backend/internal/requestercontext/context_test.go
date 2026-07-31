package requestercontext

import (
	"encoding/json"
	"reflect"
	"testing"
)

func testContext() Context {
	return Context{
		Version:        CurrentVersion,
		RequestID:      "wecom-message-1",
		PolicyRevision: "sha256:policy",
		Principal: Principal{
			Channel:         "wecom",
			BotID:           "bot-1",
			CanonicalUserID: "user-1",
			DisplayName:     "Test User",
		},
		Audience: Audience{
			ChatID:   "chat-1",
			ChatType: "group",
		},
		Authorization: Authorization{
			Capabilities: []string{
				CapabilityCASToken,
				CapabilityCMRQuery,
				CapabilityIndicatorsQuery,
				CapabilityMetricQuery,
				CapabilitySQLSelect,
			},
			Scope: Scope{
				ManageAreaIDs:     []string{"CN18"},
				DCManageAreaIDs:   []string{"CN18"},
				CategoryLevel1IDs: []string{"12", "13"},
			},
		},
	}
}

func TestContextJSONContract(t *testing.T) {
	data, err := json.Marshal(testContext())
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	for _, key := range []string{"version", "requestId", "policyRevision", "principal", "audience", "authorization"} {
		if _, ok := got[key]; !ok {
			t.Errorf("context JSON missing %q", key)
		}
	}

	principal := got["principal"].(map[string]any)
	for _, key := range []string{"channel", "botId", "canonicalUserId", "displayName"} {
		if _, ok := principal[key]; !ok {
			t.Errorf("principal JSON missing %q", key)
		}
	}
	authorization := got["authorization"].(map[string]any)
	scope := authorization["scope"].(map[string]any)
	for _, key := range []string{"manageAreaIds", "dcManageAreaIds", "categoryLevel1Ids"} {
		if _, ok := scope[key]; !ok {
			t.Errorf("scope JSON missing %q", key)
		}
	}
}

func TestCapabilityValues(t *testing.T) {
	got := []string{CapabilityCASToken, CapabilityCMRQuery, CapabilityIndicatorsQuery, CapabilityMetricQuery, CapabilitySQLSelect}
	want := []string{"qdm.cas.token", "qdm.cmr.query", "qdm.indicators.query", "qdm.metric.query", "qdm.sql.select"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("capabilities = %#v, want %#v", got, want)
	}
}

func TestPromptMeta(t *testing.T) {
	requester := testContext()
	got := PromptMeta(requester)
	lumi, ok := got["lumi"].(map[string]any)
	if !ok {
		t.Fatalf("PromptMeta()[lumi] = %#v, want map[string]any", got["lumi"])
	}
	if !reflect.DeepEqual(lumi["requesterContext"], requester) {
		t.Fatalf("requesterContext = %#v, want %#v", lumi["requesterContext"], requester)
	}

	data, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("json.Marshal(PromptMeta()) error = %v", err)
	}
	var wire map[string]any
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatalf("json.Unmarshal(PromptMeta()) error = %v", err)
	}
	wireLumi := wire["lumi"].(map[string]any)
	if _, ok := wireLumi["requesterContext"].(map[string]any); !ok {
		t.Fatalf("wire requesterContext = %#v, want object", wireLumi["requesterContext"])
	}
}
