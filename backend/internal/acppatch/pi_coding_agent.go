package acppatch

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
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

//go:embed assets/pi-coding-agent-0.83.0/*.patch
var piCodingAgentPatchFS embed.FS

var piCodingAgentPatchFiles = []string{
	"dist_core_agent-session.js.patch",
	"dist_core_extensions_runner.js.patch",
	"dist_modes_rpc_rpc-mode.js.patch",
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

// EnsurePiCodingAgentHostAuthPatched applies Lumi hostAuth patches to a pin-installed agent package.
// Patch files are embedded in the lumi binary (same maintenance model as pi-acp patches).
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

	tmpDir, err := os.MkdirTemp("", "lumi-pi-agent-patches-*")
	if err != nil {
		status.Message = err.Error()
		return status, err
	}
	defer os.RemoveAll(tmpDir)

	for _, name := range piCodingAgentPatchFiles {
		data, err := piCodingAgentPatchFS.ReadFile("assets/pi-coding-agent-0.83.0/" + name)
		if err != nil {
			err = fmt.Errorf("embedded agent patch %s: %w", name, err)
			status.Message = err.Error()
			return status, err
		}
		patchPath := filepath.Join(tmpDir, name)
		if err := os.WriteFile(patchPath, data, 0644); err != nil {
			status.Message = err.Error()
			return status, err
		}
		cmd := exec.Command("patch", "-p1", "-d", pkgDir, "--forward", "--batch", "-i", patchPath)
		out, err := cmd.CombinedOutput()
		if err != nil {
			if agentLooksPatched(pkgDir) {
				logf(opts, strings.TrimSpace(string(out)))
				continue
			}
			err = fmt.Errorf("apply %s: %w (%s)", name, err, strings.TrimSpace(string(out)))
			status.Message = err.Error()
			return status, err
		}
		logf(opts, "applied "+name)
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
	return strings.Contains(s, "setTurnHostAuth") && strings.Contains(s, "_turnHostAuth")
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
