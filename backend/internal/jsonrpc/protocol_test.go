package jsonrpc

import "testing"

func TestErrorIncludesStringData(t *testing.T) {
	t.Parallel()

	err := &Error{
		Code:    InvalidParams,
		Message: "Invalid params",
		Data:    "Unknown sessionId: session-1",
	}

	if got, want := err.Error(), "Invalid params: Unknown sessionId: session-1"; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}

func TestErrorIncludesDetailsData(t *testing.T) {
	t.Parallel()

	err := &Error{
		Code:    InvalidParams,
		Message: "Invalid params",
		Data:    map[string]any{"details": "missing field"},
	}

	if got, want := err.Error(), "Invalid params: missing field"; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}
