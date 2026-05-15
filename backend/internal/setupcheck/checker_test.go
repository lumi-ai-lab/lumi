package setupcheck

import (
	"testing"

	"github.com/pengmide/lumi/internal/config"
)

func TestInitialStatusIncludesQwenPackageAndCLI(t *testing.T) {
	t.Parallel()

	status := InitialStatus([]config.AgentConfig{
		{
			ID:      "qwen",
			Name:    "Qwen Code",
			Command: "npx",
			Args:    []string{"-y", "@qwen-code/qwen-code", "--acp"},
		},
	})

	if len(status.ACPPackages) != 1 {
		t.Fatalf("len(ACPPackages) = %d, want 1", len(status.ACPPackages))
	}
	if got := status.ACPPackages[0].Package; got != "@qwen-code/qwen-code" {
		t.Fatalf("Qwen package = %q, want @qwen-code/qwen-code", got)
	}
	if len(status.Agents) != 1 {
		t.Fatalf("len(Agents) = %d, want 1", len(status.Agents))
	}
	if got := status.Agents[0].Command; got != "qwen" {
		t.Fatalf("Qwen command = %q, want qwen", got)
	}
	if got := installInstructions["qwen"]; got != "npm install -g @qwen-code/qwen-code" {
		t.Fatalf("qwen install instruction = %q", got)
	}
}
