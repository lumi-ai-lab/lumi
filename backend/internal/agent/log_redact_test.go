package agent

import (
	"strings"
	"testing"
)

func TestRedactLogValueSuppressesInstructionAndIdentityMetadata(t *testing.T) {
	input := `{"jsonrpc":"2.0","method":"session/prompt","params":{"sessionId":"session-private","cwd":"/private/workspace","prompt":[{"type":"text","text":"private question"}],"_meta":{"lumi":{"sessionInstructions":{"baseInstructions":"private protocol","sessionContext":"private context"},"turnContext":{"text":"private history"},"requesterContext":{"requestId":"private-request","contextToken":"private-token"}}}}}`
	got := redactLogValue(input)
	for _, secret := range []string{"session-private", "/private/workspace", "private question", "private protocol", "private context", "private history", "private-request", "private-token"} {
		if strings.Contains(got, secret) {
			t.Fatalf("redacted log contains %q: %s", secret, got)
		}
	}
	for _, want := range []string{"session/prompt", "redacted"} {
		if !strings.Contains(got, want) {
			t.Fatalf("redacted log missing %q: %s", want, got)
		}
	}
}

func TestRedactLogValueSuppressesAgentContentToolInputAndPaths(t *testing.T) {
	input := `{"jsonrpc":"2.0","method":"session/update","params":{"update":{"content":{"type":"text","text":"private model output"},"toolCall":{"rawInput":{"command":"cat /private/file"},"locations":[{"path":"/private/file"}]}}}}`
	got := redactLogValue(input)
	for _, secret := range []string{"private model output", "cat /private/file", "/private/file"} {
		if strings.Contains(got, secret) {
			t.Fatalf("redacted log contains %q: %s", secret, got)
		}
	}
	if !strings.Contains(got, "session/update") {
		t.Fatalf("redacted log lost safe method name: %s", got)
	}
}
