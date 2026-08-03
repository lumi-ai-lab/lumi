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
	root := filepath.Join(t.TempDir(), "requester-context")
	t.Setenv(EnvRequesterContextRoot, root)
	t.Setenv(EnvRequesterContextReaderGID, "2003")
	settings, err := RuntimeSettingsFromEnv("ignored")
	if err != nil {
		t.Fatalf("RuntimeSettingsFromEnv() error = %v", err)
	}
	if settings.Root != root || !settings.Secure() || settings.ReaderGID == nil || *settings.ReaderGID != 2003 {
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
		{name: "relative root", root: "run/lumi/requester-context", gid: "2003", want: "absolute"},
		{name: "filesystem root", root: string(filepath.Separator), gid: "2003", want: "volume root"},
		{name: "broad run root", root: filepath.Join(string(filepath.Separator), "run"), gid: "2003", want: "basename"},
		{name: "broad var root", root: filepath.Join(string(filepath.Separator), "var"), gid: "2003", want: "basename"},
		{name: "broad opt root", root: filepath.Join(string(filepath.Separator), "opt"), gid: "2003", want: "basename"},
		{name: "broad tmp root", root: filepath.Join(string(filepath.Separator), "tmp"), gid: "2003", want: "basename"},
		{name: "wrong basename", root: filepath.Join(t.TempDir(), "contexts"), gid: "2003", want: "basename"},
		{name: "unclean root", root: t.TempDir() + string(filepath.Separator) + "runtime" + string(filepath.Separator) + ".." + string(filepath.Separator) + "requester-context", gid: "2003", want: "clean"},
		{name: "trailing separator", root: filepath.Join(t.TempDir(), "requester-context") + string(filepath.Separator), gid: "2003", want: "clean"},
		{name: "root whitespace", root: " " + filepath.Join(t.TempDir(), "requester-context"), gid: "2003", want: "whitespace"},
		{name: "invalid gid", root: "/run/lumi/requester-context", gid: "reader", want: "parse"},
		{name: "gid whitespace", root: "/run/lumi/requester-context", gid: " 2003", want: "whitespace"},
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
