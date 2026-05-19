package imagent

import (
	"context"
	"testing"
)

func TestIsStopCommand(t *testing.T) {
	tests := []struct {
		text string
		want bool
	}{
		{text: "/stop", want: true},
		{text: " /stop ", want: true},
		{text: "/stop now", want: false},
		{text: "hello /stop", want: false},
		{text: "/cancel", want: false},
		{text: "/stopping", want: false},
		{text: "", want: false},
	}

	for _, tt := range tests {
		if got := IsStopCommand(tt.text); got != tt.want {
			t.Fatalf("IsStopCommand(%q) = %v, want %v", tt.text, got, tt.want)
		}
	}
}

func TestRunRegistryStopAndUnregister(t *testing.T) {
	registry := NewRunRegistry()

	ctx, cancel := context.WithCancel(context.Background())
	token := registry.Register("conv-1", cancel)
	if token == 0 {
		t.Fatal("Register() token = 0")
	}
	if !registry.Stop("conv-1") {
		t.Fatal("Stop() = false, want true")
	}
	if ctx.Err() == nil {
		t.Fatal("registered context was not canceled")
	}
	if registry.Stop("conv-1") {
		t.Fatal("second Stop() = true, want false")
	}

	ctx, cancel = context.WithCancel(context.Background())
	token = registry.Register("conv-1", cancel)
	registry.Unregister("conv-1", token)
	if registry.Stop("conv-1") {
		t.Fatal("Stop() after Unregister = true, want false")
	}
	if ctx.Err() != nil {
		t.Fatal("unregistered context was canceled")
	}
}

func TestRunRegistryUnregisterIgnoresStaleToken(t *testing.T) {
	registry := NewRunRegistry()

	_, firstCancel := context.WithCancel(context.Background())
	firstToken := registry.Register("conv-1", firstCancel)
	secondCtx, secondCancel := context.WithCancel(context.Background())
	registry.Register("conv-1", secondCancel)

	registry.Unregister("conv-1", firstToken)
	if !registry.Stop("conv-1") {
		t.Fatal("Stop() after stale Unregister = false, want true")
	}
	if secondCtx.Err() == nil {
		t.Fatal("current context was not canceled")
	}
}
