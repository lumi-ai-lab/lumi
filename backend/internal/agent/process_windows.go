//go:build windows

package agent

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/pengmide/lumi/internal/config"
	"github.com/pengmide/lumi/internal/sysutil"
)

func configureCommand(cmd *exec.Cmd, cfg *config.AgentConfig) error {
	if cfg == nil {
		return fmt.Errorf("agent config is missing")
	}
	if err := cfg.ValidateRunAsIdentity(); err != nil {
		return err
	}
	if cfg.RunAsUID != nil {
		return fmt.Errorf("run-as agent identity is not supported on Windows")
	}
	sysutil.HideWindow(cmd)
	return nil
}

func hideWindow(cmd *exec.Cmd) {
	sysutil.HideWindow(cmd)
}

func interruptProcess(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return os.ErrProcessDone
	}
	return cmd.Process.Signal(os.Interrupt)
}

func killProcess(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return os.ErrProcessDone
	}
	return cmd.Process.Kill()
}
