package api

import (
	"strings"
	"testing"

	lumicron "github.com/pengmide/lumi/internal/cron"
)

type noopCronRunner struct{}

func (noopCronRunner) RunCronJob(job lumicron.Job) (string, error) {
	return job.ConversationID, nil
}

func TestExecuteCronCommandsWithServiceCreatesAndStripsCommand(t *testing.T) {
	t.Parallel()

	store := lumicron.NewStore(t.TempDir() + "/jobs.json")
	service := lumicron.NewService(store, noopCronRunner{}, nil)
	streamItems := []streamItem{{Type: "text", Text: "I'll set that up."}}
	currentText := `[CRON_CREATE]
name: Standup reminder
schedule: interval:60
schedule_description: Every hour
message: Ask for standup notes
[/CRON_CREATE]`

	_, err := executeCronCommandsWithService(service, cronCommandContext{
		Channel:        lumicron.ChannelWeb,
		ConversationID: "conv-1",
		AgentID:        "agent-1",
		WorkspaceID:    "workspace-1",
	}, &streamItems, &currentText)
	if err != nil {
		t.Fatalf("executeCronCommandsWithService() error = %v", err)
	}
	if strings.Contains(currentText, "[CRON_") || strings.Contains(streamItems[0].Text, "[CRON_") {
		t.Fatalf("raw cron command leaked: stream=%q current=%q", streamItems[0].Text, currentText)
	}
	if !strings.Contains(currentText, `Created scheduled task "Standup reminder"`) {
		t.Fatalf("currentText = %q, want creation confirmation", currentText)
	}
	jobs := service.List()
	if len(jobs) != 1 {
		t.Fatalf("len(jobs) = %d, want 1", len(jobs))
	}
	job := jobs[0]
	if job.ConversationID != "conv-1" || job.AgentID != "agent-1" || job.WorkspaceID != "workspace-1" {
		t.Fatalf("job context = %#v", job)
	}
}

func TestExecuteCronCommandsWithServiceReturnsConfirmationText(t *testing.T) {
	t.Parallel()

	store := lumicron.NewStore(t.TempDir() + "/jobs.json")
	service := lumicron.NewService(store, noopCronRunner{}, nil)
	_, err := service.Create(lumicron.Job{
		ID:             "cron_single",
		Name:           "Single task",
		AgentID:        "agent-1",
		WorkspaceID:    "workspace-1",
		ConversationID: "conv-1",
		Enabled:        true,
		Schedule:       lumicron.Schedule{Type: "interval", EverySeconds: 60},
		Prompt:         "hello",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	streamItems := []streamItem{}
	currentText := "[CRON_LIST]"
	confirmationText, err := executeCronCommandsWithService(service, cronCommandContext{
		Channel:        lumicron.ChannelWeb,
		ConversationID: "conv-1",
		AgentID:        "agent-1",
		WorkspaceID:    "workspace-1",
	}, &streamItems, &currentText)
	if err != nil {
		t.Fatalf("executeCronCommandsWithService() error = %v", err)
	}
	if !strings.Contains(confirmationText, "Scheduled tasks:\n- Single task (cron_single):") {
		t.Fatalf("confirmationText = %q, want task list", confirmationText)
	}
	if !strings.Contains(currentText, confirmationText) {
		t.Fatalf("currentText = %q, want appended confirmation %q", currentText, confirmationText)
	}
}

func TestExecuteCronCommandsWithServiceListsOnlyCurrentScope(t *testing.T) {
	t.Parallel()

	store := lumicron.NewStore(t.TempDir() + "/jobs.json")
	service := lumicron.NewService(store, noopCronRunner{}, nil)
	for _, job := range []lumicron.Job{
		{
			ID:             "cron_current",
			Name:           "Current task",
			Channel:        lumicron.ChannelWeb,
			ConversationID: "conv-1",
			AgentID:        "agent-1",
			WorkspaceID:    "workspace-1",
			Enabled:        true,
			Schedule:       lumicron.Schedule{Type: "interval", EverySeconds: 60},
			Prompt:         "hello",
		},
		{
			ID:             "cron_other",
			Name:           "Other task",
			Channel:        lumicron.ChannelWeb,
			ConversationID: "conv-2",
			AgentID:        "agent-1",
			WorkspaceID:    "workspace-1",
			Enabled:        true,
			Schedule:       lumicron.Schedule{Type: "interval", EverySeconds: 60},
			Prompt:         "hello",
		},
		{
			ID:             "cron_wechat",
			Name:           "WeChat task",
			Channel:        lumicron.ChannelWeChat,
			ConversationID: "conv-1",
			AgentID:        "agent-1",
			WorkspaceID:    "workspace-1",
			Enabled:        true,
			Schedule:       lumicron.Schedule{Type: "interval", EverySeconds: 60},
			Prompt:         "hello",
		},
	} {
		if _, err := service.Create(job); err != nil {
			t.Fatalf("Create(%s) error = %v", job.ID, err)
		}
	}

	streamItems := []streamItem{}
	currentText := "[CRON_LIST]"
	confirmationText, err := executeCronCommandsWithService(service, cronCommandContext{
		Channel:        lumicron.ChannelWeb,
		ConversationID: "conv-1",
		AgentID:        "agent-1",
		WorkspaceID:    "workspace-1",
	}, &streamItems, &currentText)
	if err != nil {
		t.Fatalf("executeCronCommandsWithService() error = %v", err)
	}
	if !strings.Contains(confirmationText, "Current task") {
		t.Fatalf("confirmationText = %q, want current task", confirmationText)
	}
	if strings.Contains(confirmationText, "Other task") || strings.Contains(confirmationText, "WeChat task") {
		t.Fatalf("confirmationText leaked out-of-scope jobs: %q", confirmationText)
	}
}

func TestExecuteCronCommandsWithServiceRunsForHiddenContext(t *testing.T) {
	t.Parallel()

	store := lumicron.NewStore(t.TempDir() + "/jobs.json")
	service := lumicron.NewService(store, noopCronRunner{}, nil)
	_, err := service.Create(lumicron.Job{
		ID:             "cron_single",
		Name:           "Single task",
		AgentID:        "agent-1",
		WorkspaceID:    "workspace-1",
		ConversationID: "conv-1",
		Enabled:        true,
		Schedule:       lumicron.Schedule{Type: "interval", EverySeconds: 60},
		Prompt:         "hello",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	streamItems := []streamItem{}
	currentText := "[CRON_PAUSE:cron_single]"
	_, err = executeCronCommandsWithService(service, cronCommandContext{
		Hidden:         true,
		Channel:        lumicron.ChannelWeb,
		ConversationID: "conv-1",
		AgentID:        "agent-1",
		WorkspaceID:    "workspace-1",
	}, &streamItems, &currentText)
	if err != nil {
		t.Fatalf("executeCronCommandsWithService() error = %v", err)
	}
	job, ok := service.Get("cron_single")
	if !ok || job.Enabled {
		t.Fatalf("job = %#v, %v; want paused even in hidden context", job, ok)
	}
}

func TestAcquireCronRunBlocksWhileManualChatOwnsConversation(t *testing.T) {
	server := newTestAPIServer(t)
	if !server.acquireCronRun("conv-1") {
		t.Fatalf("manual acquire failed")
	}
	if server.acquireCronRun("conv-1") {
		t.Fatalf("second acquire succeeded; want busy")
	}
	server.releaseCronRun("conv-1")
	if !server.acquireCronRun("conv-1") {
		t.Fatalf("acquire after release failed")
	}
	server.releaseCronRun("conv-1")
}

func TestExecuteCronCommandsWithServiceIgnoresHiddenWhenNoCommand(t *testing.T) {
	t.Parallel()

	store := lumicron.NewStore(t.TempDir() + "/jobs.json")
	service := lumicron.NewService(store, noopCronRunner{}, nil)
	streamItems := []streamItem{}
	currentText := "hello"
	_, err := executeCronCommandsWithService(service, cronCommandContext{
		Hidden:         true,
		Channel:        lumicron.ChannelWeb,
		ConversationID: "conv-1",
		AgentID:        "agent-1",
		WorkspaceID:    "workspace-1",
	}, &streamItems, &currentText)
	if err != nil {
		t.Fatalf("executeCronCommandsWithService() error = %v", err)
	}
	if currentText != "hello" {
		t.Fatalf("currentText = %q, want unchanged", currentText)
	}
}

func TestCronCommandStreamStateSuppressesWithoutMutatingCommandText(t *testing.T) {
	t.Parallel()

	state := &cronCommandStreamState{}
	currentText := "[CRON_LIST]"
	if !state.shouldSuppress(currentText) {
		t.Fatalf("shouldSuppress() = false, want true")
	}
	if currentText != "[CRON_LIST]" {
		t.Fatalf("currentText mutated to %q", currentText)
	}
	if state.shouldSuppress(currentText + "\nDone.") {
		t.Fatalf("shouldSuppress() kept suppressing after completed single-line command")
	}
}

func TestExecuteCronCommandsWithServiceDeletesCurrentSingleTask(t *testing.T) {
	t.Parallel()

	store := lumicron.NewStore(t.TempDir() + "/jobs.json")
	service := lumicron.NewService(store, noopCronRunner{}, nil)
	_, err := service.Create(lumicron.Job{
		ID:             "cron_single",
		Name:           "Single task",
		AgentID:        "agent-1",
		WorkspaceID:    "workspace-1",
		ConversationID: "conv-1",
		Enabled:        true,
		Schedule:       lumicron.Schedule{Type: "interval", EverySeconds: 60},
		Prompt:         "hello",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	streamItems := []streamItem{}
	currentText := "[CRON_DELETE:current]"
	_, err = executeCronCommandsWithService(service, cronCommandContext{
		Channel:        lumicron.ChannelWeb,
		ConversationID: "conv-1",
		AgentID:        "agent-1",
		WorkspaceID:    "workspace-1",
	}, &streamItems, &currentText)
	if err != nil {
		t.Fatalf("executeCronCommandsWithService() error = %v", err)
	}
	if len(service.List()) != 0 {
		t.Fatalf("job was not deleted: %#v", service.List())
	}
	if !strings.Contains(currentText, "Deleted scheduled task cron_single.") {
		t.Fatalf("currentText = %q, want delete confirmation", currentText)
	}
}

func TestExecuteCronCommandsWithServiceDeleteIsIdempotent(t *testing.T) {
	t.Parallel()

	store := lumicron.NewStore(t.TempDir() + "/jobs.json")
	service := lumicron.NewService(store, noopCronRunner{}, nil)
	_, err := service.Create(lumicron.Job{
		ID:             "cron_single",
		Name:           "Single task",
		AgentID:        "agent-1",
		WorkspaceID:    "workspace-1",
		ConversationID: "conv-1",
		Enabled:        true,
		Schedule:       lumicron.Schedule{Type: "interval", EverySeconds: 60},
		Prompt:         "hello",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	ctx := cronCommandContext{
		Channel:        lumicron.ChannelWeb,
		ConversationID: "conv-1",
		AgentID:        "agent-1",
		WorkspaceID:    "workspace-1",
	}
	streamItems := []streamItem{}
	currentText := "[CRON_DELETE:cron_single]"
	if _, err := executeCronCommandsWithService(service, ctx, &streamItems, &currentText); err != nil {
		t.Fatalf("first delete error = %v", err)
	}

	streamItems = []streamItem{}
	currentText = "[CRON_DELETE:cron_single]"
	if _, err := executeCronCommandsWithService(service, ctx, &streamItems, &currentText); err != nil {
		t.Fatalf("second delete error = %v", err)
	}
	if !strings.Contains(currentText, "Scheduled task was already deleted.") {
		t.Fatalf("currentText = %q, want idempotent confirmation", currentText)
	}
}

func TestExecuteCronCommandsWithServiceDeleteCurrentWithNoTasksDoesNotError(t *testing.T) {
	t.Parallel()

	store := lumicron.NewStore(t.TempDir() + "/jobs.json")
	service := lumicron.NewService(store, noopCronRunner{}, nil)
	streamItems := []streamItem{}
	currentText := "[CRON_DELETE:current]"
	if _, err := executeCronCommandsWithService(service, cronCommandContext{
		Channel:        lumicron.ChannelWeb,
		ConversationID: "conv-1",
		AgentID:        "agent-1",
		WorkspaceID:    "workspace-1",
	}, &streamItems, &currentText); err != nil {
		t.Fatalf("executeCronCommandsWithService() error = %v", err)
	}
	if !strings.Contains(currentText, "No scheduled tasks.") {
		t.Fatalf("currentText = %q, want no tasks confirmation", currentText)
	}
}

func TestExecuteCronCommandsWithServiceResolvesPlaceholderAndDedupes(t *testing.T) {
	t.Parallel()

	store := lumicron.NewStore(t.TempDir() + "/jobs.json")
	service := lumicron.NewService(store, noopCronRunner{}, nil)
	_, err := service.Create(lumicron.Job{
		ID:             "cron_single",
		Name:           "Single task",
		AgentID:        "agent-1",
		WorkspaceID:    "workspace-1",
		ConversationID: "conv-1",
		Enabled:        false,
		Schedule:       lumicron.Schedule{Type: "interval", EverySeconds: 60},
		Prompt:         "hello",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	streamItems := []streamItem{}
	currentText := "[CRON_RESUME:<job_id>]\n[CRON_RESUME:<job_id>]"
	_, err = executeCronCommandsWithService(service, cronCommandContext{
		Channel:        lumicron.ChannelWeb,
		ConversationID: "conv-1",
		AgentID:        "agent-1",
		WorkspaceID:    "workspace-1",
	}, &streamItems, &currentText)
	if err != nil {
		t.Fatalf("executeCronCommandsWithService() error = %v", err)
	}
	job, ok := service.Get("cron_single")
	if !ok || !job.Enabled {
		t.Fatalf("job = %#v, %v; want enabled", job, ok)
	}
	if strings.Count(currentText, `Resumed scheduled task "Single task".`) != 1 {
		t.Fatalf("currentText = %q, want one resume confirmation", currentText)
	}
}
