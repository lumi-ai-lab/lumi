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

	"github.com/pengmide/lumi/internal/fssecure"
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
	return materializePrivate(lumipaths.Path("runtime", "pi-acp-bridge"))
}

func materializePrivate(root string) (string, error) {
	return materialize(root, 0o700, 0o600, nil, false)
}

func materialize(root string, dirMode, fileMode os.FileMode, gid *uint32, strict bool) (string, error) {
	if _, err := Version(); err != nil {
		return "", err
	}
	if len(bundle) == 0 {
		return "", fmt.Errorf("embedded PI ACP bridge bundle is empty")
	}

	dir := filepath.Join(root, Signature())
	if strict {
		for _, path := range []string{root, dir} {
			if err := fssecure.EnsureDirectory(path, dirMode, gid); err != nil {
				return "", fmt.Errorf("prepare shared PI ACP bridge runtime: %w", err)
			}
		}
	} else {
		if err := os.MkdirAll(dir, dirMode); err != nil {
			return "", fmt.Errorf("create PI ACP bridge runtime: %w", err)
		}
		for _, path := range []string{root, dir} {
			if err := os.Chmod(path, dirMode); err != nil {
				return "", fmt.Errorf("secure PI ACP bridge runtime %q: %w", path, err)
			}
		}
	}

	entrypoint := filepath.Join(dir, "index.js")
	assets := []struct {
		name    string
		content []byte
	}{
		{name: "index.js", content: bundle},
		{name: "package.json", content: packageMetadata},
		{name: "LICENSE", content: bridgeLicense},
		{name: "THIRD_PARTY_NOTICES.md", content: thirdPartyNotices},
	}
	for _, asset := range assets {
		path := filepath.Join(dir, asset.name)
		var err error
		if strict {
			err = writeSharedAtomicContent(path, asset.content, fileMode, gid)
		} else {
			err = writeAtomicContent(path, asset.content, fileMode)
		}
		if err != nil {
			return "", fmt.Errorf("materialize PI ACP bridge asset %s: %w", asset.name, err)
		}
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
