//go:build !windows

package agent

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/pengmide/lumi/internal/config"
)

func configureCommand(cmd *exec.Cmd, cfg *config.AgentConfig) error {
	if cfg == nil {
		return fmt.Errorf("agent config is missing")
	}
	if err := cfg.ValidateRunAsIdentity(); err != nil {
		return err
	}

	attributes := &syscall.SysProcAttr{Setpgid: true}
	if cfg.RunAsUID != nil {
		if *cfg.RunAsUID == uint32(os.Geteuid()) {
			return fmt.Errorf("runAsUid must differ from the Lumi publisher UID")
		}
		// Use a non-nil group slice even when it is empty so the child does not
		// inherit the privileged publisher's supplementary groups.
		groups := append([]uint32{}, cfg.SupplementaryGIDs...)
		attributes.Credential = &syscall.Credential{
			Uid:    *cfg.RunAsUID,
			Gid:    *cfg.RunAsGID,
			Groups: groups,
		}
	}
	cmd.SysProcAttr = attributes
	return nil
}

func hideWindow(cmd *exec.Cmd) {
}

func interruptProcess(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return os.ErrProcessDone
	}
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGINT)
}

func killProcess(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return os.ErrProcessDone
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		return cmd.Process.Kill()
	}
	return nil
}
