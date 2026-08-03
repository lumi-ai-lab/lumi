package piacpbridge

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/pengmide/lumi/internal/fssecure"
	"github.com/pengmide/lumi/internal/lumipaths"
)

// SharedRoot places a run-as bridge beside a secure requester root, or under a
// publisher-scoped, traversable temporary root when security is not enabled.
func SharedRoot(requesterContextRoot string) (string, error) {
	if requesterContextRoot != "" {
		if !filepath.IsAbs(requesterContextRoot) || filepath.Clean(requesterContextRoot) != requesterContextRoot || filepath.Base(requesterContextRoot) != "requester-context" {
			return "", fmt.Errorf("requester-context root must be a clean absolute requester-context directory")
		}
		return filepath.Join(filepath.Dir(requesterContextRoot), "pi-acp-bridge"), nil
	}
	sum := sha256.Sum256([]byte(lumipaths.Home()))
	tempRoot := os.TempDir()
	if runtime.GOOS != "windows" {
		tempRoot = "/tmp"
	}
	return filepath.Join(tempRoot, "lumi-pi-acp-bridge-"+hex.EncodeToString(sum[:8])), nil
}

// MaterializeShared writes a group-readable, publisher-owned bridge for a PI
// launched under a different UID. Existing paths are never repaired.
func MaterializeShared(root string, gid uint32) (string, error) {
	if gid == 0 {
		return "", fmt.Errorf("shared PI ACP bridge GID must not be root")
	}
	if err := validateSharedRoot(root); err != nil {
		return "", err
	}
	return materialize(root, 0o750, 0o640, &gid, true)
}

func validateSharedRoot(root string) error {
	if strings.ContainsRune(root, '\x00') || !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return fmt.Errorf("shared PI ACP bridge root must be a clean absolute path")
	}
	base := filepath.Base(root)
	if base != "pi-acp-bridge" && !strings.HasPrefix(base, "lumi-pi-acp-bridge-") {
		return fmt.Errorf("shared PI ACP bridge root must be a dedicated pi-acp-bridge directory")
	}
	return nil
}

func writeSharedAtomicContent(path string, content []byte, mode os.FileMode, gid *uint32) error {
	if _, err := os.Lstat(path); err == nil {
		if err := fssecure.ValidateRegularFile(path, mode, gid); err != nil {
			return err
		}
		current, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !bytes.Equal(current, content) {
			return fmt.Errorf("existing content does not match embedded asset")
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".bridge-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := fssecure.SetGroup(tmpPath, gid); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	return fssecure.ValidateRegularFile(path, mode, gid)
}
