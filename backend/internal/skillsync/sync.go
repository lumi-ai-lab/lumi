package skillsync

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/pengmide/lumi/internal/agentmode"
	"github.com/pengmide/lumi/internal/skillstore"
)

// Service exposes the high-level orchestration callers use: "sync the SSOT
// to all enabled skill directories on this machine".
type Service struct {
	store    *skillstore.Store
	resolver Resolver
}

// New constructs a Service backed by the given store. The resolver is
// optional; nil falls back to a fresh skillstore.Materializer.
func New(store *skillstore.Store, resolver Resolver) *Service {
	if resolver == nil {
		resolver = skillstore.NewMaterializer(store)
	}
	return &Service{store: store, resolver: resolver}
}

// SyncLocal applies the current SSOT to ~/.claude/skills, ~/.codex/skills,
// and ~/.qwen/skills using ModeAuto. Errors per-app are collected into the
// returned map; callers decide how to surface them.
func (s *Service) SyncLocal(ctx context.Context) (map[string]Result, error) {
	if s == nil || s.store == nil {
		return nil, fmt.Errorf("skillsync: store is nil")
	}
	out := map[string]Result{}
	for _, backend := range SupportedBackends() {
		appKey := AppKey(backend)
		dir, err := UserSkillDir("", backend)
		if err != nil || dir == "" {
			continue
		}
		res, err := ApplyToDir(ApplyOptions{
			AppDir:   dir,
			AppKey:   appKey,
			Records:  s.store.List(),
			Resolver: s.resolver,
			Mode:     ModeAuto,
			Scope:    "local",
			Ctx:      ctx,
		})
		if err != nil {
			res.Errors = append(res.Errors, err.Error())
		}
		out[appKey] = res
	}
	return out, nil
}

// SyncToRoot applies the current SSOT to <root>/<dotApp>/skills paths. Used
// by sandbox staging where root is the per-workspace credential mount dir.
// dotAppDirs lets callers override default ".claude"/".codex"/".qwen" names
// (e.g., the sandbox claude-root layout uses literal ".claude").
func (s *Service) SyncToRoot(ctx context.Context, root string, mode Mode, dotApp map[Backend]string) (map[string]Result, error) {
	if s == nil || s.store == nil {
		return nil, fmt.Errorf("skillsync: store is nil")
	}
	out := map[string]Result{}
	for _, backend := range SupportedBackends() {
		appKey := AppKey(backend)
		dot := dotApp[backend]
		if dot == "" {
			dot = defaultDotApp(backend)
		}
		dir := filepath.Join(root, dot, "skills")
		res, err := ApplyToDir(ApplyOptions{
			AppDir:   dir,
			AppKey:   appKey,
			Records:  s.store.List(),
			Resolver: s.resolver,
			Mode:     mode,
			Scope:    "sandbox",
			Ctx:      ctx,
		})
		if err != nil {
			res.Errors = append(res.Errors, err.Error())
		}
		out[appKey] = res
	}
	return out, nil
}

func defaultDotApp(b Backend) string {
	switch b {
	case agentmode.BackendClaude:
		return ".claude"
	case agentmode.BackendCodex:
		return ".codex"
	case agentmode.BackendQwen:
		return ".qwen"
	default:
		return ""
	}
}
