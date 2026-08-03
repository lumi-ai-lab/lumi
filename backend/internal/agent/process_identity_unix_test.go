//go:build !windows

package agent

import (
	"os"
	"os/exec"
	"slices"
	"testing"

	"github.com/pengmide/lumi/internal/config"
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
