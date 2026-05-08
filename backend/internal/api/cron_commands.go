package api

import (
	"errors"
	"fmt"
	"strings"
	"time"

	lumicron "github.com/pengmide/lumi/internal/cron"
)

type cronCommandContext struct {
	Hidden         bool
	Channel        string
	ConversationID string
	AgentID        string
	WorkspaceID    string
	Target         lumicron.Target
}

func (s *Server) executeCronCommandsForChat(ctx chatRuntimeContext, streamItems *[]streamItem, currentText *string) error {
	confirmationText, err := s.executeCronCommands(cronCommandContext{
		Channel:        lumicron.ChannelWeb,
		ConversationID: ctx.Prepared.ConvID,
		AgentID:        ctx.Prepared.AgentID,
		WorkspaceID:    ctx.Prepared.WorkspaceID,
	}, streamItems, currentText)
	if err != nil {
		return err
	}
	if confirmationText != "" && ctx.SendEvent != nil && !ctx.Request.Hidden {
		ctx.SendEvent("update", map[string]any{
			"update": sessionUpdate{
				SessionUpdate: "agent_message_chunk",
				Content: map[string]string{
					"type": "text",
					"text": confirmationText,
				},
			},
		})
	}
	return nil
}

func (s *Server) executeCronCommands(ctx cronCommandContext, streamItems *[]streamItem, currentText *string) (string, error) {
	return executeCronCommandsWithService(s.cron, ctx, streamItems, currentText)
}

func (s *Server) currentCronJobsForPrompt(channel, conversationID string) []lumicron.Job {
	if s.cron == nil {
		return nil
	}
	return s.cron.ListByScope(channel, conversationID)
}

func executeCronCommandsWithService(cronService *lumicron.Service, ctx cronCommandContext, streamItems *[]streamItem, currentText *string) (string, error) {
	if cronService == nil {
		return "", nil
	}
	allText := strings.Builder{}
	for _, item := range *streamItems {
		if item.Type == "text" {
			allText.WriteString(item.Text)
			allText.WriteString("\n")
		}
	}
	allText.WriteString(*currentText)
	commands := lumicron.DetectCommands(allText.String())
	commands = dedupeCronCommands(commands)
	if len(commands) == 0 {
		return "", nil
	}

	for i := range *streamItems {
		if (*streamItems)[i].Type == "text" {
			(*streamItems)[i].Text = lumicron.StripCommands((*streamItems)[i].Text)
		}
	}
	*currentText = lumicron.StripCommands(*currentText)

	confirmations := make([]string, 0, len(commands))
	for _, command := range commands {
		confirmation, err := executeCronCommand(cronService, ctx, command)
		if err != nil {
			return "", err
		}
		if confirmation != "" {
			confirmations = append(confirmations, confirmation)
		}
	}
	if len(confirmations) > 0 {
		confirmationText := strings.Join(confirmations, "\n")
		if *currentText != "" {
			*currentText += "\n\n"
		}
		*currentText += confirmationText
		return confirmationText, nil
	}
	return "", nil
}

func executeCronCommand(cronService *lumicron.Service, ctx cronCommandContext, command lumicron.Command) (string, error) {
	switch command.Kind {
	case "create":
		job := lumicron.Job{
			ID:             "cron_" + generateUUID(),
			Name:           command.Name,
			Prompt:         command.Prompt,
			AgentID:        ctx.AgentID,
			WorkspaceID:    ctx.WorkspaceID,
			Channel:        ctx.Channel,
			ConversationID: ctx.ConversationID,
			Target:         ctx.Target,
			Enabled:        true,
			Schedule:       command.Schedule,
		}
		created, err := cronService.Create(job)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Created scheduled task %q. Next run: %s.", created.Name, formatCronTimestamp(created.State.NextRunAt)), nil
	case "list":
		jobs := cronService.ListByScope(ctx.Channel, ctx.ConversationID)
		if len(jobs) == 0 {
			return "No scheduled tasks.", nil
		}
		lines := []string{"Scheduled tasks:"}
		for _, job := range jobs {
			lines = append(lines, fmt.Sprintf("- %s (%s): %s", job.Name, job.ID, formatCronTimestamp(job.State.NextRunAt)))
		}
		return strings.Join(lines, "\n"), nil
	case "delete":
		if err := resolveCronCommandJobID(cronService, ctx, &command); err != nil {
			if err.Error() == "scheduled task not found" || err.Error() == "no scheduled tasks" {
				return "No scheduled tasks.", nil
			}
			return "", err
		}
		if command.JobID == "" {
			return "No scheduled tasks.", nil
		}
		existed := true
		if _, ok := cronService.GetScoped(ctx.Channel, ctx.ConversationID, command.JobID); !ok {
			existed = false
		}
		if err := cronService.DeleteScoped(ctx.Channel, ctx.ConversationID, command.JobID); err != nil {
			return "", err
		}
		if !existed {
			return "Scheduled task was already deleted.", nil
		}
		return fmt.Sprintf("Deleted scheduled task %s.", command.JobID), nil
	case "pause":
		if err := resolveCronCommandJobID(cronService, ctx, &command); err != nil {
			return "", err
		}
		updated, err := cronService.UpdateScoped(ctx.Channel, ctx.ConversationID, command.JobID, func(job lumicron.Job) (lumicron.Job, error) {
			job.Enabled = false
			return job, nil
		})
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Paused scheduled task %q.", updated.Name), nil
	case "resume":
		if err := resolveCronCommandJobID(cronService, ctx, &command); err != nil {
			return "", err
		}
		updated, err := cronService.UpdateScoped(ctx.Channel, ctx.ConversationID, command.JobID, func(job lumicron.Job) (lumicron.Job, error) {
			job.Enabled = true
			return job, nil
		})
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Resumed scheduled task %q.", updated.Name), nil
	case "run":
		if err := resolveCronCommandJobID(cronService, ctx, &command); err != nil {
			return "", err
		}
		conversationID, err := cronService.RunNowScoped(ctx.Channel, ctx.ConversationID, command.JobID)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Started scheduled task %s now in conversation %s.", command.JobID, conversationID), nil
	default:
		return "", nil
	}
}

func resolveCronCommandJobID(cronService *lumicron.Service, ctx cronCommandContext, command *lumicron.Command) error {
	id := strings.TrimSpace(command.JobID)
	if id == "" {
		return errors.New("scheduled task id is required")
	}
	if shouldResolveImplicitCronJobID(id) {
		jobs := cronService.ListByScope(ctx.Channel, ctx.ConversationID)
		if len(jobs) == 1 {
			command.JobID = jobs[0].ID
			return nil
		}
		if len(jobs) == 0 {
			return errors.New("no scheduled tasks")
		}
		return errors.New("multiple scheduled tasks exist; list tasks first and choose a task id")
	}
	if strings.HasPrefix(id, "cron_") {
		if command.Kind != "delete" {
			if _, ok := cronService.GetScoped(ctx.Channel, ctx.ConversationID, id); !ok {
				return errors.New("scheduled task not found")
			}
		}
		command.JobID = id
		return nil
	}
	if command.Kind != "delete" {
		if _, ok := cronService.GetScoped(ctx.Channel, ctx.ConversationID, id); !ok {
			return errors.New("scheduled task not found")
		}
	}
	command.JobID = id
	return nil
}

func shouldResolveImplicitCronJobID(id string) bool {
	normalized := strings.Trim(strings.ToLower(strings.TrimSpace(id)), "<>[](){} \t\r\n")
	switch normalized {
	case "job_id", "id", "current", "this", "all", "the current task", "this task", "全部", "当前", "现在", "这个", "这个任务":
		return true
	default:
		return false
	}
}

func dedupeCronCommands(commands []lumicron.Command) []lumicron.Command {
	if len(commands) < 2 {
		return commands
	}
	seen := make(map[string]struct{}, len(commands))
	deduped := make([]lumicron.Command, 0, len(commands))
	for _, command := range commands {
		key := fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%d\x00%d",
			command.Kind,
			command.JobID,
			command.Name,
			command.Prompt,
			command.Schedule.RunAt,
			command.Schedule.EverySeconds,
		)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		deduped = append(deduped, command)
	}
	return deduped
}

func formatCronTimestamp(value int64) string {
	if value == 0 {
		return "-"
	}
	return time.UnixMilli(value).Format("2006-01-02 15:04")
}

type cronCommandStreamState struct {
	suppress     bool
	commandStart int
	scanFrom     int
}

func (state *cronCommandStreamState) shouldSuppress(currentText string) bool {
	if state == nil {
		return false
	}
	if state.scanFrom > len(currentText) {
		state.scanFrom = 0
	}
	if !state.suppress {
		if idx := strings.Index(currentText[state.scanFrom:], "[CRON_"); idx >= 0 {
			state.suppress = true
			state.commandStart = state.scanFrom + idx
		}
	}
	if !state.suppress {
		state.scanFrom = len(currentText)
		return false
	}
	if state.commandStart > len(currentText) {
		state.commandStart = len(currentText)
	}
	commandText := currentText[state.commandStart:]
	if strings.Contains(commandText, "[/CRON_CREATE]") ||
		strings.Contains(commandText, "[CRON_LIST]") ||
		strings.Contains(commandText, "[CRON_DELETE:") ||
		strings.Contains(commandText, "[CRON_PAUSE:") ||
		strings.Contains(commandText, "[CRON_RESUME:") ||
		strings.Contains(commandText, "[CRON_RUN:") {
		state.suppress = false
		state.scanFrom = len(currentText)
	}
	return true
}
