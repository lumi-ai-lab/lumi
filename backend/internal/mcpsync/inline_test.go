package mcpsync

import (
	"encoding/json"
	"testing"

	"github.com/pengmide/lumi/internal/agentmode"
	"github.com/pengmide/lumi/internal/mcpstore"
)

func TestBuildSessionMCPFiltersByBackend(t *testing.T) {
	records := []mcpstore.Record{
		{
			ID: "fs", Name: "filesystem", Transport: mcpstore.TransportStdio,
			Command: "npx", Args: []string{"@modelcontextprotocol/server-filesystem", "/tmp"},
			Apps:   mcpstore.Apps{Claude: true, Codex: true, Qwen: false},
			Scopes: mcpstore.DefaultScopes(),
		},
		{
			ID: "remote", Name: "remote", Transport: mcpstore.TransportHTTP,
			URL:    "https://example.com",
			Apps:   mcpstore.Apps{Claude: false, Codex: false, Qwen: true},
			Scopes: mcpstore.DefaultScopes(),
		},
	}

	claude := BuildSessionMCP(agentmode.BackendClaude, records)
	if len(claude) != 1 || claude[0].Name != "filesystem" {
		t.Fatalf("claude = %+v", claude)
	}

	qwen := BuildSessionMCP(agentmode.BackendQwen, records)
	if len(qwen) != 1 || qwen[0].URL != "https://example.com" {
		t.Fatalf("qwen = %+v", qwen)
	}

	codex := BuildSessionMCP(agentmode.BackendCodex, records)
	if len(codex) != 1 || codex[0].Name != "filesystem" {
		t.Fatalf("codex = %+v", codex)
	}

	if got := BuildSessionMCP(agentmode.BackendUnknown, records); len(got) != 0 {
		t.Fatalf("unknown backend should yield empty: %+v", got)
	}
}

func TestBuildSessionMCPSkipsAllScopesDisabled(t *testing.T) {
	rec := mcpstore.Record{
		ID: "x", Name: "x", Transport: mcpstore.TransportStdio, Command: "echo",
		Apps:   mcpstore.Apps{Claude: true},
		Scopes: mcpstore.Scopes{},
	}
	got := BuildSessionMCP(agentmode.BackendClaude, []mcpstore.Record{rec})
	if len(got) != 0 {
		t.Fatalf("expected empty, got %+v", got)
	}
}

func TestBuildSessionMCPDefaultsTypeToStdio(t *testing.T) {
	rec := mcpstore.Record{
		ID: "x", Name: "x", Command: "echo",
		Apps:   mcpstore.Apps{Claude: true},
		Scopes: mcpstore.DefaultScopes(),
	}
	got := BuildSessionMCP(agentmode.BackendClaude, []mcpstore.Record{rec})
	if len(got) != 1 || got[0].Type != "stdio" {
		t.Fatalf("got %+v", got)
	}
}

func TestAsAnySliceMarshalsCleanly(t *testing.T) {
	rec := mcpstore.Record{
		ID: "x", Name: "x", Transport: mcpstore.TransportStdio, Command: "echo",
		Apps:   mcpstore.Apps{Claude: true},
		Scopes: mcpstore.DefaultScopes(),
	}
	servers := BuildSessionMCP(agentmode.BackendClaude, []mcpstore.Record{rec})
	any := AsAnySlice(servers)
	if len(any) != 1 {
		t.Fatalf("len = %d", len(any))
	}
	data, err := json.Marshal(any)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if want := `[{"name":"x","type":"stdio","command":"echo"}]`; string(data) != want {
		t.Fatalf("got %s, want %s", data, want)
	}
}
