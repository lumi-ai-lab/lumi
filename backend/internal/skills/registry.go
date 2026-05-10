package skills

import (
	"strings"
	"sync"
)

// Registry discovers and caches skills from configured directories.
type Registry struct {
	mu    sync.RWMutex
	dirs  []string
	cache []*Skill
}

func NewRegistry() *Registry {
	return &Registry{}
}

func (r *Registry) SetDirs(dirs []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.dirs = append([]string(nil), dirs...)
	r.cache = nil
}

func (r *Registry) Dirs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]string(nil), r.dirs...)
}

func (r *Registry) Invalidate() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cache = nil
}

func (r *Registry) ListAll() []*Skill {
	r.mu.RLock()
	if r.cache != nil {
		defer r.mu.RUnlock()
		return append([]*Skill(nil), r.cache...)
	}
	r.mu.RUnlock()

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cache != nil {
		return append([]*Skill(nil), r.cache...)
	}

	var result []*Skill
	seen := make(map[string]bool)
	for _, dir := range r.dirs {
		result = append(result, discoverSkillsInDir(dir, dir, seen, make(map[string]bool))...)
	}
	r.cache = result
	return append([]*Skill(nil), result...)
}

func (r *Registry) Resolve(name string) *Skill {
	normalized := normalizeCommandName(name)
	for _, skill := range r.ListAll() {
		if normalizeCommandName(skill.Name) == normalized {
			return skill
		}
		for _, alias := range skill.Aliases {
			if normalizeCommandName(alias) == normalized {
				return skill
			}
		}
	}
	return nil
}

func normalizeCommandName(value string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), "-", "_"))
}
