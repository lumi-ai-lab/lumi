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
const DebugHelp = "用法：/debug thinking|tools|all [on|off]"

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
	if len(parts) == 0 {
		return "", false, nil
	}
	if parts[0] == "/debug" {
		return handleDebugCommand(parts, conversationID, workspaceID, defaultAgent, store)
	}
	if parts[0] != "/agent" {
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

func handleDebugCommand(parts []string, conversationID, workspaceID, defaultAgent string, store Store) (string, bool, error) {
	if len(parts) == 1 {
		settings, err := loadDebugSettings(store, conversationID)
		if err != nil {
			return "", true, err
		}
		return fmt.Sprintf("Debug 状态：thinking=%s, tools=%s\n\n%s", onOff(settings.Thinking), onOff(settings.Tools), DebugHelp), true, nil
	}
	if len(parts) < 2 || len(parts) > 3 {
		return DebugHelp, true, nil
	}

	target := strings.ToLower(parts[1])
	if target != "thinking" && target != "tools" && target != "all" {
		return DebugHelp, true, nil
	}

	var explicit *bool
	if len(parts) == 3 {
		value, ok := parseDebugBool(parts[2])
		if !ok {
			return DebugHelp, true, nil
		}
		explicit = &value
	}

	session, err := loadOrCreateSession(store, conversationID, workspaceID, defaultAgent)
	if err != nil {
		return "", true, err
	}
	next := session.IMDebug
	action := ""
	switch target {
	case "thinking":
		if explicit != nil {
			next.Thinking = *explicit
		} else {
			next.Thinking = !next.Thinking
		}
		action = fmt.Sprintf("已%s Debug Thinking", enabledText(next.Thinking))
	case "tools":
		if explicit != nil {
			next.Tools = *explicit
		} else {
			next.Tools = !next.Tools
		}
		action = fmt.Sprintf("已%s Debug Tools", enabledText(next.Tools))
	case "all":
		enable := !(next.Thinking && next.Tools)
		if explicit != nil {
			enable = *explicit
		}
		next.Thinking = enable
		next.Tools = enable
		action = fmt.Sprintf("已%s全部 Debug", enabledText(enable))
	}

	now := time.Now().UnixMilli()
	session.IMDebug = next
	if session.WorkspaceID == "" {
		session.WorkspaceID = workspaceID
	}
	if session.ActiveAgent == "" {
		session.ActiveAgent = defaultAgent
	}
	if session.CreatedAt == 0 {
		session.CreatedAt = now
	}
	session.UpdatedAt = now
	if session.Title == "" {
		session.Title = storage.GenerateTitle(session.Messages)
	}
	if err := store.Save(session); err != nil {
		return "", true, err
	}
	return fmt.Sprintf("%s：thinking=%s, tools=%s", action, onOff(next.Thinking), onOff(next.Tools)), true, nil
}

func loadDebugSettings(store Store, conversationID string) (storage.IMDebugSettings, error) {
	if store == nil {
		return storage.IMDebugSettings{}, errors.New("conversation store is required")
	}
	session, err := store.Load(conversationID)
	if err == nil {
		return session.IMDebug, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return storage.IMDebugSettings{}, nil
	}
	return storage.IMDebugSettings{}, err
}

func loadOrCreateSession(store Store, conversationID, workspaceID, defaultAgent string) (*storage.StoredSession, error) {
	if store == nil {
		return nil, errors.New("conversation store is required")
	}
	session, err := store.Load(conversationID)
	switch {
	case err == nil:
		return session, nil
	case errors.Is(err, os.ErrNotExist):
		return storage.CreateSession(conversationID, defaultAgent, workspaceID), nil
	default:
		return nil, err
	}
}

func parseDebugBool(value string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "on", "true", "enable", "enabled":
		return true, true
	case "off", "false", "disable", "disabled":
		return false, true
	default:
		return false, false
	}
}

func onOff(value bool) string {
	if value {
		return "on"
	}
	return "off"
}

func enabledText(value bool) string {
	if value {
		return "开启"
	}
	return "关闭"
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
	session, err := loadOrCreateSession(store, conversationID, workspaceID, agentID)
	if err != nil {
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
