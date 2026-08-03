//go:build !windows

package sandbox

import (
	"os"
	"testing"
)

func sandboxTestReaderGID(t *testing.T) uint32 {
	t.Helper()
	if gid := os.Getegid(); gid != 0 {
		return uint32(gid)
	}
	if os.Geteuid() != 0 {
		t.Skip("secured requester-context test needs a non-root group or root privileges")
	}
	return 2003
}
