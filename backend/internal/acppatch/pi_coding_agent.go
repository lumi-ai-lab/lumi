package acppatch

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Pin matches production sandbox / Dockerfile.
const (
	PiCodingAgentPackage     = "@earendil-works/pi-coding-agent"
	PiCodingAgentVersion     = "0.83.0"
	PiCodingAgentPackageSpec = PiCodingAgentPackage + "@" + PiCodingAgentVersion
	PiCodingAgentHostAuthID  = "pi-coding-agent-0.83.0-host-auth"
)

// In-process string replacements for pin 0.83.0 dist files.
// Avoids depending on a `patch` binary inside the sandbox image.
type agentFilePatch struct {
	relPath string
	old     string
	new     string
}

var piCodingAgentFilePatches = []agentFilePatch{
	{
		relPath: "dist/modes/rpc/rpc-mode.js",
		old: `                void session
                    .prompt(command.message, {
                    images: command.images,
                    streamingBehavior: command.streamingBehavior,
                    source: "rpc",
                    preflightResult: (didSucceed) => {
                        if (didSucceed) {
                            preflightSucceeded = true;
                            output(success(id, "prompt"));
                        }
                    },
                })`,
		new: `                void session
                    .prompt(command.message, {
                    images: command.images,
                    streamingBehavior: command.streamingBehavior,
                    source: "rpc",
                    hostAuth: command.hostAuth,
                    preflightResult: (didSucceed) => {
                        if (didSucceed) {
                            preflightSucceeded = true;
                            output(success(id, "prompt"));
                        }
                    },
                })`,
	},
	{
		relPath: "dist/core/agent-session.js",
		old: `    async prompt(text, options) {
        const expandPromptTemplates = options?.expandPromptTemplates ?? true;
        const preflightResult = options?.preflightResult;`,
		new: `    async prompt(text, options) {
        // Lumi hostAuth: bind encrypted auth for this turn onto the extension runner
        // so context handlers can read event._auth without a file envelope.
        if (this._extensionRunner) {
            if (options?.hostAuth && typeof options.hostAuth._auth === "string") {
                this._extensionRunner.setTurnHostAuth(options.hostAuth);
            }
            else {
                this._extensionRunner.clearTurnHostAuth();
            }
        }
        const expandPromptTemplates = options?.expandPromptTemplates ?? true;
        const preflightResult = options?.preflightResult;`,
	},
	{
		relPath: "dist/core/extensions/runner.js",
		old: `    async emitContext(messages) {
        const ctx = this.createContext();
        let currentMessages = structuredClone(messages);
        for (const ext of this.extensions) {
            const handlers = ext.handlers.get("context");
            if (!handlers || handlers.length === 0)
                continue;
            for (const handler of handlers) {
                try {
                    const event = { type: "context", messages: currentMessages };
                    const handlerResult = await handler(event, ctx);`,
		new: `    setTurnHostAuth(hostAuth) {
        if (hostAuth && typeof hostAuth._auth === "string" && hostAuth._auth.startsWith("qdm1enc.") &&
            typeof hostAuth._auth_user_id === "string" && hostAuth._auth_user_id.trim()) {
            this._turnHostAuth = {
                _auth: hostAuth._auth,
                _auth_user_id: String(hostAuth._auth_user_id).trim(),
            };
        }
        else {
            this._turnHostAuth = undefined;
        }
    }
    clearTurnHostAuth() {
        this._turnHostAuth = undefined;
    }
    async emitContext(messages) {
        const ctx = this.createContext();
        let currentMessages = structuredClone(messages);
        for (const ext of this.extensions) {
            const handlers = ext.handlers.get("context");
            if (!handlers || handlers.length === 0)
                continue;
            for (const handler of handlers) {
                try {
                    const event = { type: "context", messages: currentMessages };
                    if (this._turnHostAuth) {
                        event._auth = this._turnHostAuth._auth;
                        event._auth_user_id = this._turnHostAuth._auth_user_id;
                    }
                    const handlerResult = await handler(event, ctx);`,
	},
}

func PiCodingAgentPackageDir(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = RuntimePrefix()
	}
	candidates := []string{
		filepath.Join(prefix, "lib", "node_modules", "@earendil-works", "pi-coding-agent"),
		filepath.Join(prefix, "node_modules", "@earendil-works", "pi-coding-agent"),
	}
	for _, c := range candidates {
		if _, err := os.Stat(filepath.Join(c, "package.json")); err == nil {
			return c
		}
	}
	if runtime.GOOS == "windows" {
		return filepath.Join(prefix, "node_modules", "@earendil-works", "pi-coding-agent")
	}
	return filepath.Join(prefix, "lib", "node_modules", "@earendil-works", "pi-coding-agent")
}

// EnsurePiCodingAgentHostAuthPatched applies Lumi hostAuth patches without an external `patch` binary.
func EnsurePiCodingAgentHostAuthPatched(opts RuntimeOptions) (PatchStatus, error) {
	status := PatchStatus{
		Package: PiCodingAgentPackage,
		Version: PiCodingAgentVersion,
		PatchID: PiCodingAgentHostAuthID,
	}
	pkgDir := PiCodingAgentPackageDir(opts.Prefix)
	if err := validatePiCodingAgentPackage(pkgDir); err != nil {
		status.Message = err.Error()
		return status, err
	}
	if agentMarkerExists(pkgDir) && agentLooksPatched(pkgDir) {
		status.Applied = true
		status.Message = "Installed with Lumi patch: " + PiCodingAgentHostAuthID
		return status, nil
	}

	for _, p := range piCodingAgentFilePatches {
		path := filepath.Join(pkgDir, p.relPath)
		data, err := os.ReadFile(path)
		if err != nil {
			err = fmt.Errorf("read %s: %w", p.relPath, err)
			status.Message = err.Error()
			return status, err
		}
		text := string(data)
		if strings.Contains(text, "setTurnHostAuth") && p.relPath == "dist/core/extensions/runner.js" {
			// already patched this file
			continue
		}
		if strings.Contains(text, "hostAuth: command.hostAuth") && p.relPath == "dist/modes/rpc/rpc-mode.js" {
			continue
		}
		if strings.Contains(text, "setTurnHostAuth(options.hostAuth)") && p.relPath == "dist/core/agent-session.js" {
			continue
		}
		if !strings.Contains(text, p.old) {
			// If already fully patched overall, allow idempotent success.
			if agentLooksPatched(pkgDir) {
				continue
			}
			err = fmt.Errorf("pi-coding-agent@%s %s does not match expected patch anchor", PiCodingAgentVersion, p.relPath)
			status.Message = err.Error()
			return status, err
		}
		updated := strings.Replace(text, p.old, p.new, 1)
		if updated == text {
			err = fmt.Errorf("failed to apply in-process patch to %s", p.relPath)
			status.Message = err.Error()
			return status, err
		}
		if err := os.WriteFile(path, []byte(updated), 0644); err != nil {
			status.Message = err.Error()
			return status, err
		}
		logf(opts, "applied in-process patch "+p.relPath)
	}

	if !agentLooksPatched(pkgDir) {
		err := fmt.Errorf("pi-coding-agent@%s patches applied but hostAuth markers not found", PiCodingAgentVersion)
		status.Message = err.Error()
		return status, err
	}
	if err := writeAgentMarker(pkgDir); err != nil {
		status.Message = err.Error()
		return status, err
	}
	status.Applied = true
	status.Message = "Installed with Lumi patch: " + PiCodingAgentHostAuthID
	return status, nil
}

func validatePiCodingAgentPackage(pkgDir string) error {
	data, err := os.ReadFile(filepath.Join(pkgDir, "package.json"))
	if err != nil {
		return fmt.Errorf("%s is not installed in Lumi npm runtime", PiCodingAgentPackageSpec)
	}
	var pkg packageJSON
	if err := json.Unmarshal(data, &pkg); err != nil {
		return fmt.Errorf("failed to read pi-coding-agent package metadata: %w", err)
	}
	if pkg.Version != PiCodingAgentVersion {
		return fmt.Errorf("Lumi hostAuth patch only supports pi-coding-agent@%s, got %s@%s", PiCodingAgentVersion, pkg.Name, pkg.Version)
	}
	return nil
}

func agentLooksPatched(pkgDir string) bool {
	runner, err := os.ReadFile(filepath.Join(pkgDir, "dist", "core", "extensions", "runner.js"))
	if err != nil {
		return false
	}
	s := string(runner)
	return strings.Contains(s, "setTurnHostAuth") && strings.Contains(s, "_turnHostAuth") && strings.Contains(s, "event._auth")
}

func agentMarkerExists(pkgDir string) bool {
	data, err := os.ReadFile(filepath.Join(pkgDir, ".lumi-patches", PiCodingAgentHostAuthID+".json"))
	if err != nil {
		return false
	}
	var marker markerJSON
	if err := json.Unmarshal(data, &marker); err != nil {
		return false
	}
	return marker.ID == PiCodingAgentHostAuthID && marker.Version == PiCodingAgentVersion
}

func writeAgentMarker(pkgDir string) error {
	markerDir := filepath.Join(pkgDir, ".lumi-patches")
	if err := os.MkdirAll(markerDir, 0755); err != nil {
		return err
	}
	marker := markerJSON{
		ID:        PiCodingAgentHostAuthID,
		Package:   PiCodingAgentPackage,
		Version:   PiCodingAgentVersion,
		AppliedAt: time.Now().UTC().Format(time.RFC3339),
	}
	data, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(filepath.Join(markerDir, PiCodingAgentHostAuthID+".json"), data, 0644)
}
