package api

import (
	"net/http"

	"github.com/pengmide/lumi/internal/agentmode"
	"github.com/pengmide/lumi/internal/config"
	"github.com/pengmide/lumi/internal/skills"
)

type skillInfo struct {
	Name        string   `json:"name"`
	Aliases     []string `json:"aliases,omitempty"`
	DisplayName string   `json:"displayName,omitempty"`
	Description string   `json:"description,omitempty"`
	Source      string   `json:"source"`
	SkillFile   string   `json:"skillFile"`
}

type projectSkills struct {
	Project       string      `json:"project"`
	AgentType     string      `json:"agentType"`
	WorkspacePath string      `json:"workspacePath"`
	Dirs          []string    `json:"dirs"`
	SkillCount    int         `json:"skillCount"`
	Skills        []skillInfo `json:"skills"`
}

func (s *Server) handleSkills(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	projects := make([]projectSkills, 0)
	workspaces := s.config.Workspaces
	if len(workspaces) == 0 {
		if id := s.defaultWorkspaceID(); id != "" {
			workspaces = append(workspaces, config.WorkspaceConfig{ID: id, Name: id, Path: s.resolveWorkspacePath(id)})
		}
	}

	for _, workspace := range workspaces {
		workspacePath := s.workspaceSkillPath(workspace.ID, workspace.Path)
		for _, agentCfg := range s.agentsForWorkspace(workspace.Agents) {
			dirs := skills.BuildDirs(workspacePath, agentCfg)
			registry := skills.NewRegistry()
			registry.SetDirs(dirs)
			found := registry.ListAll()
			infos := make([]skillInfo, 0, len(found))
			for _, skill := range found {
				infos = append(infos, skillInfo{
					Name:        skill.Name,
					Aliases:     skill.Aliases,
					DisplayName: skill.DisplayName,
					Description: skill.Description,
					Source:      skill.Source,
					SkillFile:   skill.SkillFile,
				})
			}
			projects = append(projects, projectSkills{
				Project:       workspace.ID,
				AgentType:     string(agentBackend(agentCfg)),
				WorkspacePath: workspacePath,
				Dirs:          dirs,
				SkillCount:    len(infos),
				Skills:        infos,
			})
		}
	}

	writeJSON(w, map[string]any{"projects": projects})
}

func (s *Server) workspaceSkillPath(workspaceID, configuredPath string) string {
	if workspaceID != "" {
		return s.resolveWorkspacePath(workspaceID)
	}
	if configuredPath != "" {
		return configuredPath
	}
	return "."
}

func (s *Server) agentsForWorkspace(workspaceAgents []string) []config.AgentConfig {
	if len(workspaceAgents) == 0 {
		return append([]config.AgentConfig(nil), s.config.Agents...)
	}
	allowed := make(map[string]struct{}, len(workspaceAgents))
	for _, id := range workspaceAgents {
		allowed[id] = struct{}{}
	}
	var result []config.AgentConfig
	for _, agentCfg := range s.config.Agents {
		if _, ok := allowed[agentCfg.ID]; ok {
			result = append(result, agentCfg)
		}
	}
	return result
}

func agentBackend(agentCfg config.AgentConfig) agentmode.Backend {
	return agentmode.DetectBackend(agentCfg.ID, agentCfg.Command, agentCfg.Args)
}

func (s *Server) handleSkillPresets(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	presets, err := skills.FetchPresets()
	if err != nil {
		writeError(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, presets)
}
