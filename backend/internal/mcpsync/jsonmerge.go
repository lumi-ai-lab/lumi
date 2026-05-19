package mcpsync

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/pengmide/lumi/internal/mcpstore"
)

// managedKey is the top-level marker tracking which mcpServers entries
// Lumi owns. We never overwrite or delete any name absent from this list.
const managedKey = "_lumiManaged"

// jsonMergeOptions parameterizes the JSON-style writers (Claude / Qwen).
type jsonMergeOptions struct {
	Path        string                 // absolute path to the settings file
	AppKey      string                 // "claude" | "qwen"
	Records     []mcpstore.Record      // full SSOT record set
	CreateIfMissing bool               // claude=false, qwen=true (qwen file rarely exists yet)
}

// applyJSONMerge writes the SSOT-managed mcpServers into a JSON-formatted
// settings file, preserving every other key untouched. When the file does
// not exist and CreateIfMissing is false, the call is a no-op (matches
// cc-switch's "Claude not initialized → skip" semantic).
func applyJSONMerge(opts jsonMergeOptions) error {
	root, err := readJSONIfExists(opts.Path)
	if err != nil {
		return err
	}
	if root == nil {
		if !opts.CreateIfMissing {
			return nil
		}
		root = map[string]any{}
	}

	enabled := filterEnabled(opts.Records, opts.AppKey)

	servers, _ := root["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}

	// Drop previously-managed entries no longer in the desired set.
	previouslyManaged := stringSliceFromAny(root[managedKey])
	for _, name := range previouslyManaged {
		if _, stillManaged := enabled[name]; !stillManaged {
			delete(servers, name)
		}
	}
	// Write the desired entries.
	managed := make([]string, 0, len(enabled))
	for name, spec := range enabled {
		servers[name] = spec
		managed = append(managed, name)
	}
	sort.Strings(managed)

	if len(servers) == 0 {
		delete(root, "mcpServers")
	} else {
		root["mcpServers"] = servers
	}
	if len(managed) == 0 {
		delete(root, managedKey)
	} else {
		root[managedKey] = managed
	}

	return writeJSONAtomic(opts.Path, root)
}

// filterEnabled returns map[name]spec for records whose Apps contain appKey.
func filterEnabled(records []mcpstore.Record, appKey string) map[string]map[string]any {
	out := make(map[string]map[string]any)
	for _, r := range records {
		if !r.Apps.IsEnabledFor(appKey) {
			continue
		}
		if !r.Scopes.Local && !r.Scopes.Sandbox && !r.Scopes.Remote {
			continue
		}
		out[r.Name] = recordToJSONSpec(r)
	}
	return out
}

func recordToJSONSpec(r mcpstore.Record) map[string]any {
	spec := map[string]any{}
	t := r.Transport
	if t == "" {
		t = mcpstore.TransportStdio
	}
	spec["type"] = string(t)
	switch t {
	case mcpstore.TransportStdio:
		spec["command"] = r.Command
		if len(r.Args) > 0 {
			spec["args"] = append([]string(nil), r.Args...)
		}
		if len(r.Env) > 0 {
			env := make(map[string]string, len(r.Env))
			for k, v := range r.Env {
				env[k] = v
			}
			spec["env"] = env
		}
	case mcpstore.TransportHTTP, mcpstore.TransportSSE:
		spec["url"] = r.URL
		if len(r.Headers) > 0 {
			h := make(map[string]string, len(r.Headers))
			for k, v := range r.Headers {
				h[k] = v
			}
			spec["headers"] = h
		}
	}
	return spec
}

func readJSONIfExists(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(data) == 0 {
		return map[string]any{}, nil
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return root, nil
}

func writeJSONAtomic(path string, root map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func stringSliceFromAny(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
