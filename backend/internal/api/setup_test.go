package api

import "testing"

func TestNormalizePackageName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		packageSpec string
		want        string
	}{
		{
			name:        "current scoped package with version",
			packageSpec: "@agentclientprotocol/claude-agent-acp@0.30.0",
			want:        "@agentclientprotocol/claude-agent-acp",
		},
		{
			name:        "current scoped package without version",
			packageSpec: "@agentclientprotocol/claude-agent-acp",
			want:        "@agentclientprotocol/claude-agent-acp",
		},
		{
			name:        "legacy scoped package with version",
			packageSpec: "@zed-industries/claude-code-acp@0.23.1",
			want:        "@zed-industries/claude-code-acp",
		},
		{
			name:        "qwen scoped package",
			packageSpec: "@qwen-code/qwen-code@1.0.0",
			want:        "@qwen-code/qwen-code",
		},
		{
			name:        "unscoped package with version",
			packageSpec: "typescript@5.9.3",
			want:        "typescript",
		},
		{
			name:        "unscoped package without version",
			packageSpec: "typescript",
			want:        "typescript",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := normalizePackageName(tt.packageSpec)
			if got != tt.want {
				t.Fatalf("normalizePackageName(%q) = %q, want %q", tt.packageSpec, got, tt.want)
			}
		})
	}
}
