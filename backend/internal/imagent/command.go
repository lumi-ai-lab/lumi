package imagent

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/pengmide/lumi/internal/config"
	"github.com/pengmide/lumi/internal/storage"
)

const FormatHelp = "格式：/agent 或 /agent <id>"

type Store interface {
	Load(id string) (*storage.StoredSession, error)
	Save(session *storage.StoredSession) error
}

func ResolveActiveAgent(store Store, conversationID, workspaceID, defaultAgent string, cfg *config.Config, workspace *config.WorkspaceConfig) (string, error) {
	if store == nil {
		return "", errors.New("conversation store is required")
	}
	available := availableAgentIDs(cfg, workspace)
	if len(available) == 0 {
		return "", errors.New("no available agents configured")
	}
	allowed := idSet(available)

	if session, err := store.Load(conversationID); err == nil {
		if allowed[session.ActiveAgent] {
			return session.ActiveAgent, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}

	defaultAgent = strings.TrimSpace(defaultAgent)
	if allowed[defaultAgent] {
		return defaultAgent, nil
	}
	return "", fmt.Errorf("default agent unavailable: %s", defaultAgent)
}

func HandleCommand(text, conversationID, workspaceID, defaultAgent string, cfg *config.Config, workspace *config.WorkspaceConfig, store Store) (string, bool, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "", false, nil
	}
	parts := strings.Fields(trimmed)
	if len(parts) == 0 || parts[0] != "/agent" {
		return "", false, nil
	}
	if len(parts) > 2 {
		return FormatHelp, true, nil
	}

	available := availableAgentIDs(cfg, workspace)
	if len(available) == 0 {
		return "", true, errors.New("no available agents configured")
	}
	allowed := idSet(available)

	current, err := ResolveActiveAgent(store, conversationID, workspaceID, defaultAgent, cfg, workspace)
	if err != nil {
		return "", true, err
	}

	if len(parts) == 1 {
		return formatList(current, available), true, nil
	}

	target := parts[1]
	if !allowed[target] {
		return fmt.Sprintf("未找到可用 Agent：%s\n\n可用 Agent：%s", target, strings.Join(available, ", ")), true, nil
	}
	if err := persistActiveAgent(store, conversationID, workspaceID, target); err != nil {
		return "", true, err
	}
	return fmt.Sprintf("已切换当前 Agent 为 %s。", target), true, nil
}

func availableAgentIDs(cfg *config.Config, workspace *config.WorkspaceConfig) []string {
	if cfg == nil {
		return nil
	}
	if workspace == nil || len(workspace.Agents) == 0 {
		ids := make([]string, 0, len(cfg.Agents))
		for _, agent := range cfg.Agents {
			if strings.TrimSpace(agent.ID) != "" {
				ids = append(ids, agent.ID)
			}
		}
		return ids
	}

	allowed := make(map[string]struct{}, len(workspace.Agents))
	for _, id := range workspace.Agents {
		if id = strings.TrimSpace(id); id != "" {
			allowed[id] = struct{}{}
		}
	}
	ids := make([]string, 0, len(allowed))
	for _, agent := range cfg.Agents {
		if _, ok := allowed[agent.ID]; ok {
			ids = append(ids, agent.ID)
		}
	}
	return ids
}

func idSet(ids []string) map[string]bool {
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	return set
}

func formatList(current string, available []string) string {
	lines := []string{
		fmt.Sprintf("当前 Agent：%s", current),
		"",
		"可用 Agent：",
	}
	for _, id := range available {
		if id == current {
			lines = append(lines, fmt.Sprintf("* %s 当前", id))
		} else {
			lines = append(lines, fmt.Sprintf("* %s", id))
		}
	}
	lines = append(lines, "", "切换：/agent <id>")
	return strings.Join(lines, "\n")
}

func persistActiveAgent(store Store, conversationID, workspaceID, agentID string) error {
	session, err := store.Load(conversationID)
	switch {
	case err == nil:
	case errors.Is(err, os.ErrNotExist):
		session = storage.CreateSession(conversationID, agentID, workspaceID)
	default:
		return err
	}
	now := time.Now().UnixMilli()
	session.ActiveAgent = agentID
	if session.WorkspaceID == "" {
		session.WorkspaceID = workspaceID
	}
	if session.CreatedAt == 0 {
		session.CreatedAt = now
	}
	session.UpdatedAt = now
	if session.Title == "" {
		session.Title = storage.GenerateTitle(session.Messages)
	}
	return store.Save(session)
}
