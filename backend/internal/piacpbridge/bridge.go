package piacpbridge

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/pengmide/lumi/internal/lumipaths"
)

// The bridge is built from the auditable thin fork in fork/. Runtime npm
// dependencies are bundled into index.js, so materialization never downloads
// or modifies third-party packages.

//go:embed fork/dist/index.js
var bundle []byte

//go:embed fork/package.json
var packageMetadata []byte

//go:embed fork/LICENSE
var bridgeLicense []byte

//go:embed fork/THIRD_PARTY_NOTICES.md
var thirdPartyNotices []byte

var (
	metadataOnce sync.Once
	version      string
	metadataErr  error
)

// Version returns the embedded bridge package version.
func Version() (string, error) {
	metadataOnce.Do(func() {
		var metadata struct {
			Version string `json:"version"`
		}
		if err := json.Unmarshal(packageMetadata, &metadata); err != nil {
			metadataErr = fmt.Errorf("parse embedded PI ACP bridge metadata: %w", err)
			return
		}
		version = strings.TrimSpace(metadata.Version)
		if version == "" {
			metadataErr = fmt.Errorf("embedded PI ACP bridge version is empty")
		}
	})
	return version, metadataErr
}

// Signature changes whenever the declared bridge version or an embedded
// runtime asset changes. It is safe to expose in setup state and logs because
// it does not contain paths, credentials, or instruction text.
func Signature() string {
	bridgeVersion, err := Version()
	if err != nil {
		bridgeVersion = "invalid"
	}
	hash := sha256.New()
	for _, content := range [][]byte{bundle, packageMetadata, bridgeLicense, thirdPartyNotices} {
		_, _ = hash.Write(content)
		_, _ = hash.Write([]byte{0})
	}
	return bridgeVersion + "-" + hex.EncodeToString(hash.Sum(nil)[:8])
}

// Materialize writes the embedded ESM bundle to a content-versioned private
// runtime directory and returns its entrypoint. Files are installed with
// atomic renames so concurrent Lumi processes cannot observe partial content.
func Materialize() (string, error) {
	if _, err := Version(); err != nil {
		return "", err
	}
	if len(bundle) == 0 {
		return "", fmt.Errorf("embedded PI ACP bridge bundle is empty")
	}

	root := lumipaths.Path("runtime", "pi-acp-bridge")
	dir := filepath.Join(root, Signature())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create PI ACP bridge runtime: %w", err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return "", fmt.Errorf("secure PI ACP bridge runtime root: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", fmt.Errorf("secure PI ACP bridge runtime: %w", err)
	}

	entrypoint := filepath.Join(dir, "index.js")
	if err := writeAtomicContent(entrypoint, bundle, 0o600); err != nil {
		return "", fmt.Errorf("materialize PI ACP bridge bundle: %w", err)
	}
	if err := writeAtomicContent(filepath.Join(dir, "package.json"), packageMetadata, 0o600); err != nil {
		return "", fmt.Errorf("materialize PI ACP bridge metadata: %w", err)
	}
	if err := writeAtomicContent(filepath.Join(dir, "LICENSE"), bridgeLicense, 0o600); err != nil {
		return "", fmt.Errorf("materialize PI ACP bridge license: %w", err)
	}
	if err := writeAtomicContent(filepath.Join(dir, "THIRD_PARTY_NOTICES.md"), thirdPartyNotices, 0o600); err != nil {
		return "", fmt.Errorf("materialize PI ACP bridge third-party notices: %w", err)
	}
	return entrypoint, nil
}

func writeAtomicContent(path string, content []byte, mode os.FileMode) error {
	if current, err := os.ReadFile(path); err == nil && bytes.Equal(current, content) {
		return os.Chmod(path, mode)
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
	return os.Rename(tmpPath, path)
}
