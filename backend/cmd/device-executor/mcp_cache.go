package main

import (
	"sync"

	"github.com/pengmide/lumi/internal/agentmode"
	"github.com/pengmide/lumi/internal/mcpstore"
	"github.com/pengmide/lumi/internal/mcpsync"
)

// mcpCache holds the latest set of SSOT MCP records pushed from the server.
// The runner consults it to inject mcpServers into ACP session/new calls,
// matching the behavior of the in-process server.
type mcpCache struct {
	mu      sync.RWMutex
	records []mcpstore.Record
}

var globalMCPCache mcpCache

// SetMCPRecords replaces the cache. Called by the SSOT handler on every push.
func SetMCPRecords(records []mcpstore.Record) {
	globalMCPCache.mu.Lock()
	defer globalMCPCache.mu.Unlock()
	globalMCPCache.records = append([]mcpstore.Record(nil), records...)
}

// MCPRecordsForBackend returns the inline mcpServers payload for the given
// agent. Returns an empty slice when the cache hasn't been populated.
func MCPRecordsForBackend(backend agentmode.Backend) []any {
	globalMCPCache.mu.RLock()
	records := append([]mcpstore.Record(nil), globalMCPCache.records...)
	globalMCPCache.mu.RUnlock()
	servers := mcpsync.BuildSessionMCP(backend, records)
	return mcpsync.AsAnySlice(servers)
}
