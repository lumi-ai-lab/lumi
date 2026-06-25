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

func TestInitialStatusIncludesPiACPAndCLI(t *testing.T) {
	t.Parallel()

	status := InitialStatus([]config.AgentConfig{
		{
			ID:      "pi",
			Name:    "PI",
			Command: "npx",
			Args:    []string{"-y", "pi-acp@0.0.27"},
		},
	})

	if len(status.ACPPackages) != 1 {
		t.Fatalf("len(ACPPackages) = %d, want 1", len(status.ACPPackages))
	}
	if got := status.ACPPackages[0].Package; got != "pi-acp@0.0.27" {
		t.Fatalf("PI ACP package = %q, want pi-acp@0.0.27", got)
	}
	if len(status.Agents) != 1 {
		t.Fatalf("len(Agents) = %d, want 1", len(status.Agents))
	}
	if got := status.Agents[0].Command; got != "pi" {
		t.Fatalf("PI command = %q, want pi", got)
	}
	if got := installInstructions["pi"]; got != "npm install -g @earendil-works/pi-coding-agent@0.78.0" {
		t.Fatalf("pi install instruction = %q", got)
	}
}

func TestCompareSemver(t *testing.T) {
	t.Parallel()

	tests := []struct {
		actual  string
		minimum string
		want    int
	}{
		{actual: "v22.19.0", minimum: "22.19.0", want: 0},
		{actual: "22.22.2", minimum: "22.19.0", want: 1},
		{actual: "22.18.9", minimum: "22.19.0", want: -1},
		{actual: "20.0.0", minimum: "22.19.0", want: -1},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.actual, func(t *testing.T) {
			t.Parallel()
			got := compareSemver(tt.actual, tt.minimum)
			if got != tt.want {
				t.Fatalf("compareSemver(%q, %q) = %d, want %d", tt.actual, tt.minimum, got, tt.want)
			}
		})
	}
}
