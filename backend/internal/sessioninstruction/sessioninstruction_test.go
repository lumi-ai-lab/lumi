package sessioninstruction

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDigestMatchesBridgeContract(t *testing.T) {
	if got, want := Digest("base", "context"), "7fe1a744b5d77979eb377f1fc27bc228e738a589b50898e1af309b41712dcdae"; got != want {
		t.Fatalf("Digest() = %q, want %q", got, want)
	}
}

func TestExplicitSupportAndNamespacedMetadataMerge(t *testing.T) {
	raw := json.RawMessage(`{"_meta":{"lumi":{"sessionInstructions":{"transportVersion":1,"systemPromptAppend":true,"rehydrateOnRestore":true,"turnContext":true}}}}`)
	support, ok := ExplicitSupportFromInitialize(raw)
	if !ok || !support.SupportsProfile() || !support.Explicit {
		t.Fatalf("support = %+v, ok = %v", support, ok)
	}
	params := map[string]any{"_meta": map[string]any{"lumi": map[string]any{"requesterContext": "preserved"}}}
	profile := NewProfile("base", "context")
	if err := ApplyProfile(params, support, profile, PhasePrompt); err != nil {
		t.Fatal(err)
	}
	if !ApplyTurnContext(params, support, "history") {
		t.Fatal("turn context was not applied")
	}
	encoded, _ := json.Marshal(params)
	text := string(encoded)
	for _, want := range []string{"requesterContext", "sessionInstructions", profile.ProfileDigest, "turnContext"} {
		if !strings.Contains(text, want) {
			t.Fatalf("metadata missing %q: %s", want, text)
		}
	}
}

func TestClaudeTransportUsesLegacySystemPromptOnlyForNewAndLoad(t *testing.T) {
	profile := NewProfile("base", "context")
	for _, phase := range []Phase{PhaseNew, PhaseLoad} {
		params := map[string]any{}
		if err := ApplyProfile(params, KnownClaudeSupport(), profile, phase); err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(params)
		if !strings.Contains(string(encoded), `"systemPrompt":{"append":"base\n\ncontext"}`) {
			t.Fatalf("phase %s metadata = %s", phase, encoded)
		}
	}
	params := map[string]any{}
	if err := ApplyProfile(params, KnownClaudeSupport(), profile, PhasePrompt); err != nil {
		t.Fatal(err)
	}
	if len(params) != 0 {
		t.Fatalf("Claude prompt metadata = %#v", params)
	}
}

func TestUnknownAdapterFailsClosed(t *testing.T) {
	err := ApplyProfile(map[string]any{}, Support{}, NewProfile("base", "context"), PhaseNew)
	if err == nil || !strings.Contains(err.Error(), "does not support") {
		t.Fatalf("error = %v", err)
	}
}
