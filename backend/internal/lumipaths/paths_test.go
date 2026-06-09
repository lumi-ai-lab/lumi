package lumipaths

import (
	"path/filepath"
	"testing"
)

func TestHomeDefaultsToDotLumiUnderHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", "")
	t.Setenv("LUMI_HOME", "")

	if got, want := Home(), filepath.Join(home, ".lumi"); got != want {
		t.Fatalf("Home() = %q, want %q", got, want)
	}
}

func TestHomeUsesLUMIHome(t *testing.T) {
	lumiHome := filepath.Join(t.TempDir(), "lumi-data")
	t.Setenv("LUMI_HOME", lumiHome)

	if got := Path("runtime", "sandboxes.json"); got != filepath.Join(lumiHome, "runtime", "sandboxes.json") {
		t.Fatalf("Path() = %q", got)
	}
}
