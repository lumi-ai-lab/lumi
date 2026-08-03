package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateRunAsIdentity(t *testing.T) {
	uid := uint32(2001)
	gid := uint32(2002)
	root := uint32(0)

	tests := []struct {
		name    string
		agent   AgentConfig
		wantErr string
	}{
		{name: "absent", agent: AgentConfig{}},
		{name: "complete", agent: AgentConfig{RunAsUID: &uid, RunAsGID: &gid, SupplementaryGIDs: []uint32{2003}}},
		{name: "missing gid", agent: AgentConfig{RunAsUID: &uid}, wantErr: "configured together"},
		{name: "missing uid", agent: AgentConfig{RunAsGID: &gid}, wantErr: "configured together"},
		{name: "root uid", agent: AgentConfig{RunAsUID: &root, RunAsGID: &gid}, wantErr: "must not be root"},
		{name: "root primary gid", agent: AgentConfig{RunAsUID: &uid, RunAsGID: &root}, wantErr: "must not be root"},
		{name: "root supplementary gid", agent: AgentConfig{RunAsUID: &uid, RunAsGID: &gid, SupplementaryGIDs: []uint32{0}}, wantErr: "root group"},
		{name: "groups without identity", agent: AgentConfig{SupplementaryGIDs: []uint32{2003}}, wantErr: "requires"},
		{name: "duplicate supplementary group", agent: AgentConfig{RunAsUID: &uid, RunAsGID: &gid, SupplementaryGIDs: []uint32{2003, 2003}}, wantErr: "duplicate"},
		{name: "primary repeated as supplementary", agent: AgentConfig{RunAsUID: &uid, RunAsGID: &gid, SupplementaryGIDs: []uint32{gid}}, wantErr: "duplicate"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.agent.ValidateRunAsIdentity()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateRunAsIdentity() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("ValidateRunAsIdentity() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestLoadAndSavePreservesRunAsIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lumi.config.json")
	original := `{
  "agents": [{
    "id": "pi",
    "name": "PI",
    "command": "pi-acp",
    "runAsUid": 2001,
    "runAsGid": 2002,
    "supplementaryGids": [2003]
  }],
  "defaultAgent": "pi"
}`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	pi := cfg.FindAgent("pi")
	if pi == nil || pi.RunAsUID == nil || *pi.RunAsUID != 2001 || pi.RunAsGID == nil || *pi.RunAsGID != 2002 || !pi.HasRunAsGroup(2003) {
		t.Fatalf("loaded PI identity = %+v", pi)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if err := cfg.Save(path); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	var saved struct {
		Agents []AgentConfig `json:"agents"`
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	var savedPI *AgentConfig
	for i := range saved.Agents {
		if saved.Agents[i].ID == "pi" {
			savedPI = &saved.Agents[i]
		}
	}
	if savedPI == nil || savedPI.RunAsUID == nil || *savedPI.RunAsUID != 2001 || savedPI.RunAsGID == nil || *savedPI.RunAsGID != 2002 || !savedPI.HasRunAsGroup(2003) {
		t.Fatalf("saved PI identity = %+v", savedPI)
	}
}
