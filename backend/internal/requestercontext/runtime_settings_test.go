package requestercontext

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeSettingsFromEnvDefaultsToLegacyMode(t *testing.T) {
	t.Setenv(EnvRequesterContextRoot, "")
	t.Setenv(EnvRequesterContextReaderGID, "")
	settings, err := RuntimeSettingsFromEnv("relative/default")
	if err != nil {
		t.Fatalf("RuntimeSettingsFromEnv() error = %v", err)
	}
	if settings.Root != "relative/default" || settings.Secure() || len(settings.BridgeOptions()) != 0 {
		t.Fatalf("settings = %+v, want legacy defaults", settings)
	}
}

func TestRuntimeSettingsFromEnvEnablesSecureMode(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime", "..", "requester-context")
	t.Setenv(EnvRequesterContextRoot, root)
	t.Setenv(EnvRequesterContextReaderGID, "2003")
	settings, err := RuntimeSettingsFromEnv("ignored")
	if err != nil {
		t.Fatalf("RuntimeSettingsFromEnv() error = %v", err)
	}
	if settings.Root != filepath.Clean(root) || !settings.Secure() || settings.ReaderGID == nil || *settings.ReaderGID != 2003 {
		t.Fatalf("settings = %+v", settings)
	}
}

func TestRuntimeSettingsFromEnvRejectsPartialAndInvalidSettings(t *testing.T) {
	tests := []struct {
		name string
		root string
		gid  string
		want string
	}{
		{name: "root only", root: "/run/lumi/requester-context", want: "configured together"},
		{name: "gid only", gid: "2003", want: "configured together"},
		{name: "relative root", root: "run/lumi/requester-context", gid: "2003", want: "absolute path"},
		{name: "invalid gid", root: "/run/lumi/requester-context", gid: "reader", want: "parse"},
		{name: "root gid", root: "/run/lumi/requester-context", gid: "0", want: "root group"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(EnvRequesterContextRoot, tt.root)
			t.Setenv(EnvRequesterContextReaderGID, tt.gid)
			_, err := RuntimeSettingsFromEnv("default")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("RuntimeSettingsFromEnv() error = %v, want containing %q", err, tt.want)
			}
		})
	}
}
