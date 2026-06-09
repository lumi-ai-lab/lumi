package acppatch

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/pengmide/lumi/internal/lumipaths"
)

const (
	PiACPPackage        = "pi-acp"
	PiACPVersion        = "0.0.27"
	PiACPPackageSpec    = PiACPPackage + "@" + PiACPVersion
	PiACPMultiSessionID = "pi-acp-0.0.27-multi-session"
)

const piACPSourceFile = "dist/index.js"

type RuntimeOptions struct {
	Prefix   string
	Registry string
	Log      func(string)
}

type PatchStatus struct {
	Package string
	Version string
	PatchID string
	Applied bool
	Message string
}

type packageJSON struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type markerJSON struct {
	ID        string `json:"id"`
	Package   string `json:"package"`
	Version   string `json:"version"`
	AppliedAt string `json:"appliedAt"`
}

const helperOld = `var pkg = readNearestPackageJson(import.meta.url);
var PiAcpAgent = class {`

const helperNew = `var pkg = readNearestPackageJson(import.meta.url);
function shouldUseSingleLiveSession() {
  return process.env.PI_ACP_SINGLE_LIVE_SESSION === "true";
}
var PiAcpAgent = class {`

const closeAllOld = `this.sessions.closeAllExcept?.(session.sessionId);`

const closeAllNew = `if (shouldUseSingleLiveSession()) {
      this.sessions.closeAllExcept?.(session.sessionId);
    }`

func IsTargetPiACP(packageSpec string) bool {
	return strings.TrimSpace(packageSpec) == PiACPPackageSpec
}

func RuntimePrefix() string {
	if prefix := strings.TrimSpace(os.Getenv("LUMI_NPM_RUNTIME_PREFIX")); prefix != "" {
		return prefix
	}
	if prefix := strings.TrimSpace(os.Getenv("NPM_CONFIG_PREFIX")); prefix == "/lumi/runtime/npm" {
		return prefix
	}
	return filepath.Join(lumipaths.Home(), "runtime", "shared", "runtime", "npm")
}

func PackageDir(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = RuntimePrefix()
	}
	for _, candidate := range packageDirCandidates(prefix) {
		if _, err := os.Stat(filepath.Join(candidate, "package.json")); err == nil {
			return candidate
		}
	}
	if runtime.GOOS == "windows" {
		return filepath.Join(prefix, "node_modules", PiACPPackage)
	}
	return filepath.Join(prefix, "lib", "node_modules", PiACPPackage)
}

func ExecutablePath(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = RuntimePrefix()
	}
	if runtime.GOOS == "windows" {
		return filepath.Join(prefix, "pi-acp.cmd")
	}
	return filepath.Join(prefix, "bin", "pi-acp")
}

func Status(opts RuntimeOptions) PatchStatus {
	status := PatchStatus{Package: PiACPPackage, Version: PiACPVersion, PatchID: PiACPMultiSessionID}
	pkgDir := PackageDir(opts.Prefix)
	if _, err := os.Stat(filepath.Join(pkgDir, "package.json")); err != nil {
		status.Message = "Not installed"
		return status
	}
	if err := validatePackage(pkgDir); err != nil {
		status.Message = err.Error()
		return status
	}
	applied, err := isPatched(pkgDir)
	if err != nil {
		status.Message = err.Error()
		return status
	}
	if applied && !markerExists(pkgDir) {
		status.Message = "Installed but Lumi patch marker is missing"
		return status
	}
	status.Applied = applied
	if applied {
		status.Message = "Installed with Lumi patch: " + PiACPMultiSessionID
	} else {
		status.Message = "Installed but Lumi patch is not applied"
	}
	return status
}

func EnsurePiACPPatched(opts RuntimeOptions) (PatchStatus, error) {
	status := PatchStatus{Package: PiACPPackage, Version: PiACPVersion, PatchID: PiACPMultiSessionID}
	pkgDir := PackageDir(opts.Prefix)
	if err := validatePackage(pkgDir); err != nil {
		status.Message = err.Error()
		return status, err
	}
	if err := applyPatch(pkgDir); err != nil {
		status.Message = err.Error()
		return status, err
	}
	status.Applied = true
	status.Message = "Installed with Lumi patch: " + PiACPMultiSessionID
	return status, nil
}

func InstallAndPatch(opts RuntimeOptions) (PatchStatus, error) {
	prefix := strings.TrimSpace(opts.Prefix)
	if prefix == "" {
		prefix = RuntimePrefix()
	}
	if prefix == "" {
		return PatchStatus{Package: PiACPPackage, Version: PiACPVersion, PatchID: PiACPMultiSessionID}, errors.New("failed to resolve Lumi npm runtime prefix")
	}
	registry := strings.TrimSpace(opts.Registry)
	args := []string{"install", "-g", "--prefix", prefix}
	if registry != "" {
		args = append(args, "--registry="+registry)
	}
	args = append(args, PiACPPackageSpec)
	logf(opts, "Running: npm "+strings.Join(args, " "))
	cmd := exec.Command("npm", args...)
	output, err := cmd.CombinedOutput()
	outputStr := strings.TrimSpace(string(output))
	if outputStr != "" {
		for _, line := range strings.Split(outputStr, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				logf(opts, line)
			}
		}
	}
	if err != nil {
		if outputStr != "" {
			return PatchStatus{Package: PiACPPackage, Version: PiACPVersion, PatchID: PiACPMultiSessionID}, fmt.Errorf("failed to install %s: %s", PiACPPackageSpec, outputStr)
		}
		return PatchStatus{Package: PiACPPackage, Version: PiACPVersion, PatchID: PiACPMultiSessionID}, fmt.Errorf("failed to install %s: %w", PiACPPackageSpec, err)
	}
	return EnsurePiACPPatched(RuntimeOptions{Prefix: prefix, Registry: opts.Registry, Log: opts.Log})
}

func ResolvePiACPExecutable(opts RuntimeOptions) (string, error) {
	pkgDir := PackageDir(opts.Prefix)
	if _, err := os.Stat(filepath.Join(pkgDir, "package.json")); err != nil {
		if _, installErr := InstallAndPatch(opts); installErr != nil {
			return "", installErr
		}
	} else if _, err := EnsurePiACPPatched(opts); err != nil {
		return "", err
	}
	exe := ExecutablePath(opts.Prefix)
	if _, err := os.Stat(exe); err != nil {
		return "", fmt.Errorf("patched pi-acp executable not found at %s", exe)
	}
	return exe, nil
}

func packageDirCandidates(prefix string) []string {
	return []string{
		filepath.Join(prefix, "lib", "node_modules", PiACPPackage),
		filepath.Join(prefix, "node_modules", PiACPPackage),
	}
}

func validatePackage(pkgDir string) error {
	data, err := os.ReadFile(filepath.Join(pkgDir, "package.json"))
	if err != nil {
		return fmt.Errorf("%s is not installed in Lumi npm runtime", PiACPPackageSpec)
	}
	var pkg packageJSON
	if err := json.Unmarshal(data, &pkg); err != nil {
		return fmt.Errorf("failed to read pi-acp package metadata: %w", err)
	}
	if pkg.Name != PiACPPackage || pkg.Version != PiACPVersion {
		return fmt.Errorf("Lumi PI ACP patch only supports pi-acp@0.0.27, got %s@%s", pkg.Name, pkg.Version)
	}
	return nil
}

func applyPatch(pkgDir string) error {
	target := filepath.Join(pkgDir, piACPSourceFile)
	data, err := os.ReadFile(target)
	if err != nil {
		return fmt.Errorf("failed to read pi-acp source: %w", err)
	}
	source := string(data)
	hasNew := strings.Contains(source, helperNew) && strings.Count(source, closeAllNew) == 2
	hasOld := strings.Contains(source, helperOld) && strings.Count(source, closeAllOld) == 2
	switch {
	case hasNew:
		return writeMarker(pkgDir)
	case hasOld:
		source = strings.Replace(source, helperOld, helperNew, 1)
		source = strings.ReplaceAll(source, closeAllOld, closeAllNew)
		if strings.Count(source, closeAllNew) != 2 || strings.Count(source, helperNew) != 1 {
			return errors.New("pi-acp@0.0.27 source does not match Lumi patch expectations")
		}
		if err := os.WriteFile(target, []byte(source), 0644); err != nil {
			return fmt.Errorf("failed to write patched pi-acp source: %w", err)
		}
		return writeMarker(pkgDir)
	default:
		return errors.New("pi-acp@0.0.27 source does not match Lumi patch expectations")
	}
}

func isPatched(pkgDir string) (bool, error) {
	data, err := os.ReadFile(filepath.Join(pkgDir, piACPSourceFile))
	if err != nil {
		return false, fmt.Errorf("failed to read pi-acp source: %w", err)
	}
	source := string(data)
	if strings.Contains(source, helperNew) && strings.Count(source, closeAllNew) == 2 {
		return true, nil
	}
	if strings.Contains(source, helperOld) && strings.Count(source, closeAllOld) == 2 {
		return false, nil
	}
	return false, errors.New("pi-acp@0.0.27 source does not match Lumi patch expectations")
}

func writeMarker(pkgDir string) error {
	markerDir := filepath.Join(pkgDir, ".lumi-patches")
	if err := os.MkdirAll(markerDir, 0755); err != nil {
		return fmt.Errorf("failed to create patch marker directory: %w", err)
	}
	marker := markerJSON{
		ID:        PiACPMultiSessionID,
		Package:   PiACPPackage,
		Version:   PiACPVersion,
		AppliedAt: time.Now().UTC().Format(time.RFC3339),
	}
	data, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode patch marker: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(filepath.Join(markerDir, PiACPMultiSessionID+".json"), data, 0644); err != nil {
		return fmt.Errorf("failed to write patch marker: %w", err)
	}
	return nil
}

func markerExists(pkgDir string) bool {
	data, err := os.ReadFile(filepath.Join(pkgDir, ".lumi-patches", PiACPMultiSessionID+".json"))
	if err != nil {
		return false
	}
	var marker markerJSON
	if err := json.Unmarshal(data, &marker); err != nil {
		return false
	}
	return marker.ID == PiACPMultiSessionID && marker.Package == PiACPPackage && marker.Version == PiACPVersion
}

func logf(opts RuntimeOptions, message string) {
	if opts.Log != nil {
		opts.Log(message)
	}
}
