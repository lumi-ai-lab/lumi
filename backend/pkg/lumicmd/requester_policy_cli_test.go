package lumicmd

import (
	"testing"
	"time"
)

func TestParseRequesterRefreshDuration(t *testing.T) {
	d, err := parseRequesterRefreshDuration("")
	if err != nil || d != 0 {
		t.Fatalf("empty: %v %v", d, err)
	}
	d, err = parseRequesterRefreshDuration("0")
	if err != nil || d != -1 {
		t.Fatalf("zero disable: %v %v", d, err)
	}
	d, err = parseRequesterRefreshDuration("30s")
	if err != nil || d != 30*time.Second {
		t.Fatalf("30s: %v %v", d, err)
	}
	if _, err := parseRequesterRefreshDuration("nope"); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestFormatRequesterRefresh(t *testing.T) {
	if got := formatRequesterRefresh(-1); got == "" {
		t.Fatal("disabled format empty")
	}
	if got := formatRequesterRefresh(0); got != "10m (default)" {
		t.Fatalf("default format = %q", got)
	}
	if got := formatRequesterRefresh(time.Minute); got != "1m0s" {
		t.Fatalf("custom format = %q", got)
	}
}
