package agentmode

import "testing"

func TestResolveSessionMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		backend        Backend
		sessionMode    string
		permissionMode string
		want           string
	}{
		{
			name:        "claude explicit session mode wins",
			backend:     BackendClaude,
			sessionMode: ClaudeModeAcceptEdits,
			want:        ClaudeModeAcceptEdits,
		},
		{
			name:           "claude legacy bypass maps to bypass permissions",
			backend:        BackendClaude,
			permissionMode: "bypass",
			want:           ClaudeModeBypassPermissions,
		},
		{
			name:           "codex legacy bypass maps to yolo",
			backend:        BackendCodex,
			permissionMode: "bypass",
			want:           CodexModeYolo,
		},
		{
			name:           "qwen legacy bypass maps to yolo",
			backend:        BackendQwen,
			permissionMode: "bypass",
			want:           QwenModeYolo,
		},
		{
			name:    "default fallback",
			backend: BackendClaude,
			want:    ModeDefault,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := ResolveSessionMode(tt.backend, tt.sessionMode, tt.permissionMode)
			if got != tt.want {
				t.Fatalf("ResolveSessionMode() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDetectBackendRecognizesQwen(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		id      string
		command string
		args    []string
	}{
		{name: "id", id: "qwen", command: "npx"},
		{name: "package arg", command: "npx", args: []string{"-y", "@qwen-code/qwen-code", "--acp"}},
		{name: "qwen code command", command: "qwen-code", args: []string{"--acp"}},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := DetectBackend(tt.id, tt.command, tt.args); got != BackendQwen {
				t.Fatalf("DetectBackend() = %q, want %q", got, BackendQwen)
			}
		})
	}
}

func TestAvailableModesForQwen(t *testing.T) {
	t.Parallel()

	got := AvailableModes(BackendQwen)
	if len(got) != 2 || got[0].Value != ModeDefault || got[1].Value != QwenModeYolo {
		t.Fatalf("AvailableModes(BackendQwen) = %+v, want default/yolo", got)
	}
}

func TestSelectAllowOption(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		options []PermissionOption
		want    string
		ok      bool
	}{
		{
			name: "prefers bypass permissions",
			options: []PermissionOption{
				{OptionID: "allow_once", Kind: "allow_once"},
				{OptionID: "bypassPermissions", Kind: "allow_always"},
			},
			want: "bypassPermissions",
			ok:   true,
		},
		{
			name: "falls back to allow always",
			options: []PermissionOption{
				{OptionID: "allow_always", Kind: "allow_always"},
				{OptionID: "reject_once", Kind: "reject_once"},
			},
			want: "allow_always",
			ok:   true,
		},
		{
			name: "falls back to first allow kind",
			options: []PermissionOption{
				{OptionID: "approve", Kind: "allow_custom"},
			},
			want: "approve",
			ok:   true,
		},
		{
			name: "returns false when no allow option exists",
			options: []PermissionOption{
				{OptionID: "reject_once", Kind: "reject_once"},
			},
			ok: false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := SelectAllowOption(tt.options)
			if ok != tt.ok {
				t.Fatalf("SelectAllowOption() ok = %v, want %v", ok, tt.ok)
			}
			if got != tt.want {
				t.Fatalf("SelectAllowOption() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestShouldSetACPMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		backend Backend
		mode    string
		want    bool
	}{
		{
			name:    "claude accept edits is sent to ACP",
			backend: BackendClaude,
			mode:    ClaudeModeAcceptEdits,
			want:    true,
		},
		{
			name:    "claude bypass permissions stays client-side",
			backend: BackendClaude,
			mode:    ClaudeModeBypassPermissions,
			want:    false,
		},
		{
			name:    "claude default is not sent",
			backend: BackendClaude,
			mode:    ModeDefault,
			want:    false,
		},
		{
			name:    "qwen yolo is sent to ACP",
			backend: BackendQwen,
			mode:    QwenModeYolo,
			want:    true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := ShouldSetACPMode(tt.backend, tt.mode); got != tt.want {
				t.Fatalf("ShouldSetACPMode() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsAutoApproveModeIncludesQwenYolo(t *testing.T) {
	t.Parallel()

	if !IsAutoApproveMode(BackendQwen, QwenModeYolo) {
		t.Fatal("IsAutoApproveMode(BackendQwen, yolo) = false, want true")
	}
}
