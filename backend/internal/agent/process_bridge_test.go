package agent

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/pengmide/lumi/internal/config"
	"github.com/pengmide/lumi/internal/piacpbridge"
)

func TestProcessCommandRedirectsOnlyExactBuiltInPIACP(t *testing.T) {
	t.Setenv("LUMI_HOME", t.TempDir())
	builtIn := &config.AgentConfig{ID: "pi", Command: "npx", Args: []string{"-y", config.PiACPPackageSpec}}

	command, args, err := processCommand(builtIn)
	if err != nil {
		t.Fatal(err)
	}
	if command != "node" || len(args) != 1 {
		t.Fatalf("processCommand() = %q %#v", command, args)
	}
	if filepath.Base(args[0]) != "index.js" || !filepath.IsAbs(args[0]) {
		t.Fatalf("embedded entrypoint = %q", args[0])
	}
	if !filepath.IsAbs(args[0]) || filepath.Base(filepath.Dir(args[0])) != piacpbridge.Signature() {
		t.Fatalf("entrypoint is not in the versioned bridge runtime: %q", args[0])
	}

	custom := &config.AgentConfig{ID: "pi", Command: "npx", Args: []string{"-y", "pi-acp@custom"}}
	command, args, err = processCommand(custom)
	if err != nil {
		t.Fatal(err)
	}
	if command != custom.Command || !reflect.DeepEqual(args, custom.Args) {
		t.Fatalf("custom adapter changed to %q %#v", command, args)
	}

	customLayout := &config.AgentConfig{ID: "pi", Command: "npx", Args: []string{config.PiACPPackageSpec}}
	command, args, err = processCommand(customLayout)
	if err != nil {
		t.Fatal(err)
	}
	if command != customLayout.Command || !reflect.DeepEqual(args, customLayout.Args) {
		t.Fatalf("custom argument layout changed to %q %#v", command, args)
	}
}

func TestSessionInstructionSupportRequiresExplicitCapabilityExceptClaude(t *testing.T) {
	pi := NewProcess(&config.AgentConfig{ID: "pi", Command: "npx", Args: []string{"-y", config.PiACPPackageSpec}})
	if pi.SessionInstructionSupport().SupportsProfile() {
		t.Fatal("PI support was guessed before initialize capability")
	}
	pi.captureInitializeResult(json.RawMessage(`{"_meta":{"lumi":{"sessionInstructions":{"transportVersion":1,"systemPromptAppend":true,"rehydrateOnRestore":true,"turnContext":true}}}}`))
	if support := pi.SessionInstructionSupport(); !support.SupportsProfile() || !support.Explicit || !support.Capability.TurnContext {
		t.Fatalf("PI support = %+v", support)
	}

	unknown := NewProcess(&config.AgentConfig{ID: "custom", Command: "custom-acp"})
	unknown.captureInitializeResult(json.RawMessage(`{}`))
	if unknown.SessionInstructionSupport().SupportsProfile() {
		t.Fatal("unknown adapter support was guessed")
	}

	claude := NewProcess(&config.AgentConfig{ID: "claude", Command: "npx", Args: []string{"@anthropics/claude-code", "--acp"}})
	claude.captureInitializeResult(json.RawMessage(`{}`))
	if support := claude.SessionInstructionSupport(); !support.SupportsProfile() || support.Explicit {
		t.Fatalf("Claude compatibility support = %+v", support)
	}
	claude.captureInitializeResult(json.RawMessage(`{"_meta":{"lumi":{"sessionInstructions":{"transportVersion":1,"systemPromptAppend":false,"rehydrateOnRestore":false}}}}`))
	if support := claude.SessionInstructionSupport(); support.SupportsProfile() || !support.Explicit {
		t.Fatalf("Claude explicit negative capability was ignored: %+v", support)
	}
}
