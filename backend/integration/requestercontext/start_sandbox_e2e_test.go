package requestercontext_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const legacyE2EWorkspaceID = "cli-sandbox-requester-context-e2e"

func TestStartSandboxE2ERefusesPreexistingLegacyWorkspace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the E2E launcher requires Bash")
	}

	fixture := prepareSandboxE2EFixture(t)
	fakeBin := t.TempDir()
	writeExecutable(t, filepath.Join(fakeBin, "docker"), `#!/usr/bin/env bash
case "${1:-}" in
  ps)
    printf '%s\n' 'legacy-container-id'
    ;;
  inspect)
    printf '%s\n' '`+legacyE2EWorkspaceID+`'
    ;;
  *)
    exit 2
    ;;
esac
`)
	marker := filepath.Join(t.TempDir(), "lumi-called")
	writeExecutable(t, filepath.Join(fixture, "lumi"), `#!/usr/bin/env bash
touch "$LUMI_TEST_MARKER"
`)

	cmd := exec.Command("bash", filepath.Join(fixture, "start-sandbox-e2e.sh"))
	cmd.Env = sandboxE2ETestEnv(map[string]string{
		"PATH":             fakeBin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"LUMI_TEST_MARKER": marker,
	})
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("start-sandbox-e2e.sh error = nil, output = %s", output)
	}
	if !strings.Contains(string(output), legacyE2EWorkspaceID) || !strings.Contains(string(output), "refusing to start") {
		t.Fatalf("start-sandbox-e2e.sh output = %q, want legacy workspace refusal", output)
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatalf("Lumi was invoked despite pre-existing Sandbox, stat error = %v", statErr)
	}
}

func TestStartSandboxE2EGeneratesUniqueSandboxID(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the E2E launcher requires Bash")
	}

	fixture := prepareSandboxE2EFixture(t)
	fakeBin := t.TempDir()
	writeExecutable(t, filepath.Join(fakeBin, "docker"), `#!/usr/bin/env bash
if [[ "${1:-}" == "ps" ]]; then
  exit 0
fi
exit 2
`)
	writeExecutable(t, filepath.Join(fixture, "lumi"), `#!/usr/bin/env bash
printf '%s\n' "$@" > "$LUMI_TEST_CAPTURE"
`)

	first := runSandboxE2ELauncher(t, fixture, fakeBin, "first-args")
	second := runSandboxE2ELauncher(t, fixture, fakeBin, "second-args")
	firstID := flagValue(t, first, "--sandbox-id")
	secondID := flagValue(t, second, "--sandbox-id")
	for _, sandboxID := range []string{firstID, secondID} {
		if !strings.HasPrefix(sandboxID, "requester-context-e2e-") || sandboxID == "requester-context-e2e" {
			t.Fatalf("sandbox ID = %q, want unique requester-context-e2e-* value", sandboxID)
		}
	}
	if firstID == secondID {
		t.Fatalf("consecutive E2E launches reused Sandbox ID %q", firstID)
	}
}

func prepareSandboxE2EFixture(t *testing.T) string {
	t.Helper()
	fixture := t.TempDir()
	script, err := os.ReadFile("start-sandbox-e2e.sh")
	if err != nil {
		t.Fatalf("ReadFile(start-sandbox-e2e.sh) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(fixture, "start-sandbox-e2e.sh"), script, 0o700); err != nil {
		t.Fatalf("WriteFile(start-sandbox-e2e.sh) error = %v", err)
	}
	for _, dir := range []string{"policy", "workspace"} {
		if err := os.MkdirAll(filepath.Join(fixture, dir), 0o700); err != nil {
			t.Fatalf("MkdirAll(%s) error = %v", dir, err)
		}
	}
	for path, data := range map[string]string{
		"runtime.env":                          "LUMI_BOT_ID='bot-test'\nLUMI_BOT_SECRET='secret-test'\n",
		"policy/wecom-requesters.runtime.json": "{}\n",
		"lumi.config.json":                     "{}\n",
	} {
		if err := os.WriteFile(filepath.Join(fixture, path), []byte(data), 0o600); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", path, err)
		}
	}
	writeExecutable(t, filepath.Join(fixture, "lumi"), "#!/usr/bin/env bash\nexit 0\n")
	return fixture
}

func runSandboxE2ELauncher(t *testing.T, fixture, fakeBin, captureName string) []string {
	t.Helper()
	capture := filepath.Join(t.TempDir(), captureName)
	cmd := exec.Command("bash", filepath.Join(fixture, "start-sandbox-e2e.sh"))
	cmd.Env = sandboxE2ETestEnv(map[string]string{
		"PATH":              fakeBin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"LUMI_TEST_CAPTURE": capture,
	})
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("start-sandbox-e2e.sh error = %v, output = %s", err, output)
	}
	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", capture, err)
	}
	return strings.Fields(string(data))
}

func flagValue(t *testing.T, args []string, flag string) string {
	t.Helper()
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag {
			return args[i+1]
		}
	}
	t.Fatalf("flag %s not found in args %#v", flag, args)
	return ""
}

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", path, err)
	}
}

func sandboxE2ETestEnv(overrides map[string]string) []string {
	blocked := map[string]struct{}{
		"LUMI_E2E_ALLOW_FOREIGN_SANDBOX_REMOVAL": {},
	}
	for key := range overrides {
		blocked[key] = struct{}{}
	}
	env := make([]string, 0, len(os.Environ())+len(overrides))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if _, skip := blocked[key]; !skip {
			env = append(env, entry)
		}
	}
	for key, value := range overrides {
		env = append(env, key+"="+value)
	}
	return env
}
