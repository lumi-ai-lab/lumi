//go:build windows

package sandbox

import "testing"

func sandboxTestReaderGID(t *testing.T) uint32 {
	t.Helper()
	t.Skip("numeric requester-context ownership is unsupported on Windows")
	return 0
}
