//go:build !windows

package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/pengmide/lumi/internal/config"
	"github.com/pengmide/lumi/internal/piruntime"
)

func prepareAgentRuntime(agentCfg *config.AgentConfig) error {
	if agentCfg == nil || !config.IsBuiltInPIACP(*agentCfg) || agentCfg.RunAsUID == nil {
		return nil
	}
	if err := agentCfg.ValidateRunAsIdentity(); err != nil {
		return err
	}
	if strings.TrimSpace(agentCfg.Env["HOME"]) != piruntime.SandboxHome {
		return nil
	}
	if strings.TrimSpace(agentCfg.Env[piruntime.EnvPiAgentDir]) != piruntime.SandboxAgentDir {
		return fmt.Errorf("sandbox run-as Pi agent directory does not match the secured runtime contract")
	}
	if strings.TrimSpace(agentCfg.Env[piruntime.EnvPiCredentialSource]) != piruntime.SandboxCredentialSource {
		return fmt.Errorf("sandbox run-as Pi credential source does not match the secured runtime contract")
	}
	delete(agentCfg.Env, piruntime.EnvPiCredentialSource)
	return prepareRunAsPiHome(
		piruntime.SandboxCredentialSource,
		piruntime.SandboxHome,
		*agentCfg.RunAsUID,
		*agentCfg.RunAsGID,
	)
}

func prepareRunAsPiHome(source, home string, uid, gid uint32) error {
	sourceInfo, err := os.Lstat(source)
	if err != nil {
		return fmt.Errorf("inspect sandbox run-as Pi credential source: %w", err)
	}
	if !sourceInfo.IsDir() || sourceInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("sandbox run-as Pi credential source is not a real directory")
	}
	if info, err := os.Lstat(home); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("sandbox run-as Pi home is not a real directory")
		}
		return secureRunAsPiHome(home, uid, gid)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect sandbox run-as Pi home: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(home), 0o755); err != nil {
		return fmt.Errorf("prepare sandbox run-as Pi home parent: %w", err)
	}
	temporaryHome, err := os.MkdirTemp(filepath.Dir(home), ".pi-home-")
	if err != nil {
		return fmt.Errorf("prepare sandbox run-as Pi temporary home: %w", err)
	}
	defer os.RemoveAll(temporaryHome)
	if err := os.Chmod(temporaryHome, 0o700); err != nil {
		return err
	}
	if err := copyRunAsPiCredentialTree(source, temporaryHome); err != nil {
		return fmt.Errorf("copy sandbox run-as Pi credential source: %w", err)
	}
	if err := secureRunAsPiHome(temporaryHome, uid, gid); err != nil {
		return err
	}
	if err := os.Rename(temporaryHome, home); err != nil {
		return fmt.Errorf("publish sandbox run-as Pi home: %w", err)
	}
	return nil
}

func copyRunAsPiCredentialTree(source, target string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil || relative == "." {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("credential source contains a symlink")
		}
		targetPath := filepath.Join(target, relative)
		if entry.IsDir() {
			return os.MkdirAll(targetPath, 0o700)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("credential source contains an unsupported file type")
		}
		sourceFile, err := os.Open(path)
		if err != nil {
			return err
		}
		targetFile, err := os.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			sourceFile.Close()
			return err
		}
		_, copyErr := io.Copy(targetFile, sourceFile)
		sourceCloseErr := sourceFile.Close()
		closeErr := targetFile.Close()
		if copyErr != nil {
			return copyErr
		}
		if sourceCloseErr != nil {
			return sourceCloseErr
		}
		return closeErr
	})
}

func secureRunAsPiHome(home string, uid, gid uint32) error {
	sessionsDir := filepath.Join(home, ".pi", "agent", "sessions")
	if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
		return fmt.Errorf("prepare sandbox run-as Pi home: %w", err)
	}
	return filepath.WalkDir(home, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("sandbox run-as Pi home contains a symlink")
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		mode := os.FileMode(0o600)
		if info.IsDir() {
			mode = 0o700
		} else if !info.Mode().IsRegular() {
			return fmt.Errorf("sandbox run-as Pi home contains an unsupported file type")
		}
		if err := os.Chown(path, int(uid), int(gid)); err != nil {
			return fmt.Errorf("assign sandbox run-as Pi home ownership: %w", err)
		}
		if err := os.Chmod(path, mode); err != nil {
			return fmt.Errorf("secure sandbox run-as Pi home permissions: %w", err)
		}
		return nil
	})
}
