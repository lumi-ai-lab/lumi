package agentmode

import (
	"os"
	"path/filepath"
	"strings"
)

type Backend string

const (
	BackendUnknown Backend = ""
	BackendClaude  Backend = "claude"
	BackendCodex   Backend = "codex"
	BackendQwen    Backend = "qwen"
)

const (
	ModeDefault = "default"

	ClaudeModeAcceptEdits       = "acceptEdits"
	ClaudeModeAuto              = "auto"
	ClaudeModeBypassPermissions = "bypassPermissions"
	ClaudeModeDontAsk           = "dontAsk"
	ClaudeModePlan              = "plan"

	CodexModeYolo          = "yolo"
	CodexModeYoloNoSandbox = "yoloNoSandbox"

	QwenModeYolo = "yolo"
)

type ModeOption struct {
	Value       string `json:"value"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

type PermissionOption struct {
	OptionID string
	Kind     string
}

var modeRegistry = map[Backend][]ModeOption{
	BackendClaude: {
		{Value: ModeDefault, Label: "Default", Description: "Prompt for permissions when needed."},
		{Value: ClaudeModeAcceptEdits, Label: "Accept Edits", Description: "Auto-approve file edits, but still confirm riskier actions."},
		{Value: ClaudeModeAuto, Label: "Auto", Description: "Use Claude's safety classifier for permission prompts."},
		{Value: ClaudeModeBypassPermissions, Label: "YOLO", Description: "Bypass permission prompts for all tools."},
		{Value: ClaudeModeDontAsk, Label: "Don't Ask", Description: "Block non-preapproved actions instead of prompting."},
		{Value: ClaudeModePlan, Label: "Plan", Description: "Planning mode with no tool execution."},
	},
	BackendCodex: {
		{Value: ModeDefault, Label: "Default", Description: "Manual approvals with the standard sandbox."},
		{Value: CodexModeYolo, Label: "Full Auto", Description: "Auto-approve all tool calls with workspace-write sandbox."},
		{Value: CodexModeYoloNoSandbox, Label: "Full Auto (No Sandbox)", Description: "Auto-approve all tool calls with danger-full-access sandbox."},
	},
	BackendQwen: {
		{Value: ModeDefault, Label: "Default", Description: "Prompt for permissions when needed."},
		{Value: QwenModeYolo, Label: "YOLO", Description: "Auto-approve supported Qwen tool calls."},
	},
}

func DetectBackend(id, command string, args []string) Backend {
	idLower := strings.ToLower(strings.TrimSpace(id))
	commandLower := strings.ToLower(strings.TrimSpace(command))
	argsLower := strings.ToLower(strings.Join(args, " "))
	haystack := commandLower + " " + argsLower

	switch {
	case strings.Contains(commandLower, "@qwen-code/qwen-code"), strings.Contains(haystack, "@qwen-code/qwen-code"):
		return BackendQwen
	case strings.Contains(commandLower, "qwen-code"), strings.Contains(haystack, "qwen-code"):
		return BackendQwen
	case strings.Contains(commandLower, "codex-acp"), strings.Contains(haystack, "codex-acp"):
		return BackendCodex
	case strings.Contains(commandLower, "claude-agent-acp"), strings.Contains(haystack, "claude-agent-acp"):
		return BackendClaude
	case idLower == "codex":
		return BackendCodex
	case idLower == "claude":
		return BackendClaude
	case idLower == "qwen":
		return BackendQwen
	default:
		return BackendUnknown
	}
}

func AvailableModes(backend Backend) []ModeOption {
	options := modeRegistry[backend]
	if len(options) == 0 {
		return nil
	}

	cloned := make([]ModeOption, len(options))
	copy(cloned, options)
	return cloned
}

func SupportsMode(backend Backend, mode string) bool {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		return false
	}

	for _, option := range modeRegistry[backend] {
		if option.Value == mode {
			return true
		}
	}

	return backend == BackendUnknown && mode == ModeDefault
}

func ResolveSessionMode(backend Backend, sessionMode, permissionMode string) string {
	if mode := strings.TrimSpace(sessionMode); mode != "" {
		return mode
	}

	switch backend {
	case BackendClaude:
		if strings.TrimSpace(permissionMode) == "bypass" {
			return ClaudeModeBypassPermissions
		}
	case BackendCodex:
		if strings.TrimSpace(permissionMode) == "bypass" {
			return CodexModeYolo
		}
	case BackendQwen:
		if strings.TrimSpace(permissionMode) == "bypass" {
			return QwenModeYolo
		}
	}

	return ModeDefault
}

func LegacyPermissionMode(backend Backend, sessionMode string) string {
	mode := strings.TrimSpace(sessionMode)
	if mode == "" || mode == ModeDefault {
		return ModeDefault
	}

	switch backend {
	case BackendClaude:
		if mode == ClaudeModeBypassPermissions {
			return "bypass"
		}
	case BackendCodex:
		if mode == CodexModeYolo || mode == CodexModeYoloNoSandbox {
			return "bypass"
		}
	case BackendQwen:
		if mode == QwenModeYolo {
			return "bypass"
		}
	}

	return ""
}

func SupportsACPSetMode(backend Backend) bool {
	return backend == BackendClaude || backend == BackendQwen
}

func ShouldSetACPMode(backend Backend, sessionMode string) bool {
	mode := strings.TrimSpace(sessionMode)
	if mode == "" || mode == ModeDefault || !SupportsACPSetMode(backend) {
		return false
	}

	// Claude's bypassPermissions mode requires the underlying Claude session to
	// be launched with --dangerously-skip-permissions. Lumi keeps this as a
	// client-side auto-approval mode instead of sending session/set_mode.
	return !(backend == BackendClaude && mode == ClaudeModeBypassPermissions)
}

func IsAutoApproveMode(backend Backend, sessionMode string) bool {
	mode := ResolveSessionMode(backend, sessionMode, "")
	switch backend {
	case BackendClaude:
		return mode == ClaudeModeBypassPermissions
	case BackendCodex:
		return mode == CodexModeYolo || mode == CodexModeYoloNoSandbox
	case BackendQwen:
		return mode == QwenModeYolo
	default:
		return false
	}
}

func CodexSandboxMode(sessionMode string) string {
	if strings.TrimSpace(sessionMode) == CodexModeYoloNoSandbox {
		return "danger-full-access"
	}
	return "workspace-write"
}

func PrepareSessionMode(backend Backend, sessionMode string) error {
	if backend != BackendCodex {
		return nil
	}

	return writeCodexSandboxMode(CodexSandboxMode(sessionMode))
}

func SelectAllowOption(options []PermissionOption) (string, bool) {
	if len(options) == 0 {
		return "", false
	}

	priorities := []string{"bypassPermissions", "allow_always", "allow_once"}
	for _, priority := range priorities {
		for _, option := range options {
			if option.OptionID == priority {
				return option.OptionID, true
			}
		}
	}

	for _, option := range options {
		if strings.HasPrefix(option.Kind, "allow") {
			return option.OptionID, true
		}
	}

	return "", false
}

func codexConfigPath() string {
	if home := strings.TrimSpace(os.Getenv("CODEX_HOME")); home != "" {
		return filepath.Join(home, "config.toml")
	}

	userHome, _ := os.UserHomeDir()
	return filepath.Join(userHome, ".codex", "config.toml")
}
