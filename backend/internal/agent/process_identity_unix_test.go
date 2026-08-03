//go:build !windows

package agent

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/pengmide/lumi/internal/config"
	"github.com/pengmide/lumi/internal/requestercontext"
)

func TestConfigureCommandSetsExactRunAsIdentity(t *testing.T) {
	uid := distinctProcessTestUID()
	gid := uint32(2002)
	cfg := &config.AgentConfig{
		RunAsUID:          &uid,
		RunAsGID:          &gid,
		SupplementaryGIDs: []uint32{2003, 2004},
	}
	cmd := exec.Command("unused")
	if err := configureCommand(cmd, cfg); err != nil {
		t.Fatalf("configureCommand() error = %v", err)
	}
	credential := cmd.SysProcAttr.Credential
	if credential == nil {
		t.Fatal("Credential = nil")
	}
	if credential.Uid != uid || credential.Gid != gid || !slices.Equal(credential.Groups, cfg.SupplementaryGIDs) {
		t.Fatalf("Credential = %+v, want UID %d GID %d groups %v", credential, uid, gid, cfg.SupplementaryGIDs)
	}
}

func TestProcessCommandMaterializesSharedBridgeForRunAsPI(t *testing.T) {
	parent := t.TempDir()
	requesterRoot := filepath.Join(parent, "requester-context")
	readerGID := uint32(os.Getgid())
	if readerGID == 0 {
		readerGID = 62202
	}
	t.Setenv(requestercontext.EnvRequesterContextRoot, requesterRoot)
	t.Setenv(requestercontext.EnvRequesterContextReaderGID, fmt.Sprint(readerGID))
	uid := distinctProcessTestUID()
	cfg := &config.AgentConfig{
		ID:       "pi",
		Command:  "npx",
		Args:     []string{"-y", config.PiACPPackageSpec},
		RunAsUID: &uid,
		RunAsGID: &readerGID,
	}
	command, args, err := processCommand(cfg)
	if err != nil {
		t.Fatalf("processCommand() error = %v", err)
	}
	if command != "node" || len(args) != 1 {
		t.Fatalf("processCommand() = %q %#v", command, args)
	}
	wantRoot := filepath.Join(parent, "pi-acp-bridge")
	if !strings.HasPrefix(args[0], wantRoot+string(filepath.Separator)) {
		t.Fatalf("shared bridge path = %q, want beneath %q", args[0], wantRoot)
	}
	for _, path := range []string{wantRoot, filepath.Dir(args[0])} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o750 {
			t.Fatalf("%s mode = %o, want 750", path, info.Mode().Perm())
		}
	}
	if info, err := os.Stat(args[0]); err != nil {
		t.Fatal(err)
	} else if info.Mode().Perm() != 0o640 {
		t.Fatalf("entrypoint mode = %o, want 640", info.Mode().Perm())
	}
}

func TestConfigureCommandClearsInheritedGroups(t *testing.T) {
	uid := distinctProcessTestUID()
	gid := uint32(2002)
	cmd := exec.Command("unused")
	if err := configureCommand(cmd, &config.AgentConfig{RunAsUID: &uid, RunAsGID: &gid}); err != nil {
		t.Fatalf("configureCommand() error = %v", err)
	}
	groups := cmd.SysProcAttr.Credential.Groups
	if groups == nil || len(groups) != 0 {
		t.Fatalf("Credential.Groups = %#v, want non-nil empty slice", groups)
	}
}

func distinctProcessTestUID() uint32 {
	current := uint32(os.Geteuid())
	if current == ^uint32(0) {
		return current - 1
	}
	return current + 1
}
