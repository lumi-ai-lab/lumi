// Package skillstore manages the SSOT JSON for skill records (~/.lumi/skills.json),
// plus source materialization that turns each record into a single absolute
// directory the distribution layer can read from.
package skillstore

import (
	"fmt"
	"strings"
)

// SourceType identifies where the skill content originates.
type SourceType string

const (
	SourceLocal   SourceType = "local"
	SourceGit     SourceType = "git"
	SourceArchive SourceType = "archive"
)

// Source describes how to obtain the skill directory contents.
type Source struct {
	Type      SourceType `json:"type"`
	Path      string     `json:"path,omitempty"`      // local: absolute path to skill dir
	URL       string     `json:"url,omitempty"`       // git: clone URL
	Ref       string     `json:"ref,omitempty"`       // git: branch / tag / commit
	Subdir    string     `json:"subdir,omitempty"`    // git/archive: path within repo
	UploadKey string     `json:"uploadKey,omitempty"` // archive: name under ~/.lumi/skills/_archives
}

// Apps mirrors the per-backend enable flags for skills.
type Apps struct {
	Claude bool `json:"claude"`
	Codex  bool `json:"codex"`
	Qwen   bool `json:"qwen"`
}

// IsEnabledFor reports whether the skill is enabled for backend.
func (a Apps) IsEnabledFor(backend string) bool {
	switch strings.ToLower(strings.TrimSpace(backend)) {
	case "claude":
		return a.Claude
	case "codex":
		return a.Codex
	case "qwen":
		return a.Qwen
	default:
		return false
	}
}

// SetEnabledFor sets the per-backend flag (no-op for unknown backends).
func (a *Apps) SetEnabledFor(backend string, enabled bool) {
	switch strings.ToLower(strings.TrimSpace(backend)) {
	case "claude":
		a.Claude = enabled
	case "codex":
		a.Codex = enabled
	case "qwen":
		a.Qwen = enabled
	}
}

// Scopes mirrors the per-deployment-surface flags for distribution.
type Scopes struct {
	Local   bool `json:"local"`
	Sandbox bool `json:"sandbox"`
	Remote  bool `json:"remote"`
}

// DefaultScopes enables all surfaces.
func DefaultScopes() Scopes { return Scopes{Local: true, Sandbox: true, Remote: true} }

// Record is a single skill entry persisted to skills.json.
type Record struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"displayName,omitempty"`
	Description string `json:"description,omitempty"`
	Source      Source `json:"source"`
	Apps        Apps   `json:"apps"`
	Scopes      Scopes `json:"scopes"`
	CreatedAt   int64  `json:"createdAt"`
	UpdatedAt   int64  `json:"updatedAt"`
}

// Validate sanity-checks fields prior to save.
func (r *Record) Validate() error {
	if strings.TrimSpace(r.ID) == "" {
		return errInvalidf("id is required")
	}
	if strings.TrimSpace(r.Name) == "" {
		return errInvalidf("name is required")
	}
	switch r.Source.Type {
	case SourceLocal:
		if strings.TrimSpace(r.Source.Path) == "" {
			return errInvalidf("local source requires path")
		}
	case SourceGit:
		if strings.TrimSpace(r.Source.URL) == "" {
			return errInvalidf("git source requires url")
		}
	case SourceArchive:
		if strings.TrimSpace(r.Source.UploadKey) == "" {
			return errInvalidf("archive source requires uploadKey")
		}
	default:
		return errInvalidf("unsupported source type: %s", r.Source.Type)
	}
	return nil
}

// Clone returns a deep copy.
func (r Record) Clone() Record { return r }

func errInvalidf(format string, args ...any) error {
	return &validationError{msg: fmt.Sprintf(format, args...)}
}

type validationError struct{ msg string }

func (e *validationError) Error() string { return e.msg }
