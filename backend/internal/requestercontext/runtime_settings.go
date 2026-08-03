package requestercontext

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	// EnvRequesterContextRoot selects the stable root used by a secured Lumi
	// deployment. It must be paired with EnvRequesterContextReaderGID.
	EnvRequesterContextRoot = "LUMI_REQUESTER_CONTEXT_ROOT"
	// EnvRequesterContextReaderGID identifies the dedicated group that may read
	// requester-context files. It is resolved by deployment, never hard-coded.
	EnvRequesterContextReaderGID = "LUMI_REQUESTER_CONTEXT_READER_GID"
)

// RuntimeSettings describes how a Lumi process publishes requester context.
// Secure mode is enabled only when both deployment environment variables are
// present, preventing a partially configured trust boundary.
type RuntimeSettings struct {
	Root      string
	ReaderGID *uint32
}

// Secure reports whether stable group-readable requester-context publishing is
// enabled for this process.
func (settings RuntimeSettings) Secure() bool {
	return settings.ReaderGID != nil
}

// BridgeOptions returns the FileBridge options represented by these settings.
func (settings RuntimeSettings) BridgeOptions() []FileBridgeOption {
	if settings.ReaderGID == nil {
		return nil
	}
	return []FileBridgeOption{WithReaderGID(*settings.ReaderGID)}
}

// RuntimeSettingsFromEnv resolves optional secure deployment settings. The
// default root is preserved when secure mode is not configured.
func RuntimeSettingsFromEnv(defaultRoot string) (RuntimeSettings, error) {
	rootValue, rootSet := os.LookupEnv(EnvRequesterContextRoot)
	gidValue, gidSet := os.LookupEnv(EnvRequesterContextReaderGID)
	rootValue = strings.TrimSpace(rootValue)
	gidValue = strings.TrimSpace(gidValue)
	rootSet = rootSet && rootValue != ""
	gidSet = gidSet && gidValue != ""
	if rootSet != gidSet {
		return RuntimeSettings{}, fmt.Errorf("%s and %s must be configured together", EnvRequesterContextRoot, EnvRequesterContextReaderGID)
	}
	if !rootSet {
		return RuntimeSettings{Root: defaultRoot}, nil
	}
	if !filepath.IsAbs(rootValue) {
		return RuntimeSettings{}, fmt.Errorf("%s must be an absolute path", EnvRequesterContextRoot)
	}

	parsed, err := strconv.ParseUint(gidValue, 10, 32)
	if err != nil {
		return RuntimeSettings{}, fmt.Errorf("parse %s: %w", EnvRequesterContextReaderGID, err)
	}
	gid := uint32(parsed)
	if gid == 0 {
		return RuntimeSettings{}, fmt.Errorf("%s must not be the root group", EnvRequesterContextReaderGID)
	}
	return RuntimeSettings{Root: filepath.Clean(rootValue), ReaderGID: &gid}, nil
}
