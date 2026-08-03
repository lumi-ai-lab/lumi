package requestercontext

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func testContext() Context {
	return Context{
		Version:        CurrentContextVersion,
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
				"com.example.reports.read",
				"com.example.reports.export",
			},
			Claims: Claims{
				"com.example.reports": json.RawMessage(`{"schemaVersion":1,"tenantIds":["tenant-a"]}`),
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
	if _, ok := authorization["scope"]; ok {
		t.Fatal("authorization JSON unexpectedly contains legacy scope")
	}
	claims, ok := authorization["claims"].(map[string]any)
	if !ok {
		t.Fatalf("authorization claims = %#v, want object", authorization["claims"])
	}
	if _, ok := claims["com.example.reports"].(map[string]any); !ok {
		t.Fatalf("namespaced claim = %#v, want object", claims["com.example.reports"])
	}
}

func TestAuthorizationCloneIsIndependentAndNormalizesEmptyCollections(t *testing.T) {
	original := testContext().Authorization
	cloned := original.Clone()
	cloned.Capabilities[0] = "com.example.changed.read"
	cloned.Claims["com.example.reports"][0] = 'X'
	if original.Capabilities[0] != "com.example.reports.read" || original.Claims["com.example.reports"][0] != '{' {
		t.Fatalf("Clone() mutated original authorization: %#v", original)
	}

	empty := (Authorization{}).Clone()
	data, err := json.Marshal(empty)
	if err != nil {
		t.Fatalf("json.Marshal(empty Clone()) error = %v", err)
	}
	if string(data) != `{"capabilities":[],"claims":{}}` {
		t.Fatalf("empty authorization JSON = %s", data)
	}
}

func TestNormalizeAuthorizationValidatesOnlyGenericStructure(t *testing.T) {
	original := Authorization{
		Capabilities: []string{" com.example.reports.read ", "org.example.audit.export"},
		Claims: Claims{
			"com.example.reports": json.RawMessage(`{"domainOwnedField":true}`),
			"org.example.audit":   json.RawMessage(` {"levels":["summary"]} `),
		},
	}
	normalized, err := NormalizeAuthorization(original)
	if err != nil {
		t.Fatalf("NormalizeAuthorization() error = %v", err)
	}
	if normalized.Capabilities[0] != "com.example.reports.read" || len(normalized.Claims) != 2 {
		t.Fatalf("normalized authorization = %#v", normalized)
	}
	normalized.Claims["com.example.reports"][0] = 'X'
	if original.Claims["com.example.reports"][0] != '{' {
		t.Fatal("NormalizeAuthorization() did not clone claim payload")
	}
}

func TestNormalizeAuthorizationRejectsInvalidGenericStructure(t *testing.T) {
	tests := []struct {
		name          string
		authorization Authorization
		want          string
	}{
		{name: "capability without namespace", authorization: Authorization{Capabilities: []string{"reports-read"}}, want: "invalid namespaced capability"},
		{name: "duplicate capability", authorization: Authorization{Capabilities: []string{"com.example.read", " com.example.read "}}, want: "duplicate capability"},
		{name: "invalid claim namespace", authorization: Authorization{Claims: Claims{"reports": json.RawMessage(`{}`)}}, want: "invalid namespace"},
		{name: "non-object claim", authorization: Authorization{Claims: Claims{"com.example.reports": json.RawMessage(`[]`)}}, want: "must be a JSON object"},
		{name: "invalid JSON claim", authorization: Authorization{Claims: Claims{"com.example.reports": json.RawMessage(`{"broken":`)}}, want: "must be a JSON object"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NormalizeAuthorization(tt.authorization)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("NormalizeAuthorization() error = %v, want substring %q", err, tt.want)
			}
		})
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
