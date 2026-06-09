package lumipaths

import (
	"os"
	"path/filepath"
	"strings"
)

// Home returns Lumi's data/config root. LUMI_HOME can relocate all global Lumi
// state; the default remains ~/.lumi for backwards compatibility.
func Home() string {
	if value := strings.TrimSpace(os.Getenv("LUMI_HOME")); value != "" {
		if !filepath.IsAbs(value) {
			if abs, err := filepath.Abs(value); err == nil {
				value = abs
			}
		}
		return filepath.Clean(value)
	}
	home := os.Getenv("HOME")
	if home == "" {
		home = os.Getenv("USERPROFILE")
	}
	if home == "" {
		home = "."
	}
	return filepath.Join(home, ".lumi")
}

func Path(elem ...string) string {
	parts := append([]string{Home()}, elem...)
	return filepath.Join(parts...)
}

func LegacyConfigPath() string {
	home := os.Getenv("HOME")
	if home == "" {
		home = os.Getenv("USERPROFILE")
	}
	if home == "" {
		home = "."
	}
	return filepath.Join(home, ".config", "lumi", "config.json")
}
