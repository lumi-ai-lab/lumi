package cron

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type testRunner struct {
	conversationID string
	err            error
	calls          chan Job
}

func (r testRunner) RunCronJob(job Job) (string, error) {
	if r.calls != nil {
		r.calls <- job
	}
	return r.conversationID, r.err
}

func TestRunNowUpdatesSuccessStateAndDisablesOnce(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "jobs.json"))
	events := make([]Event, 0)
	service := NewService(store, testRunner{conversationID: "conv-1"}, func(event Event) {
		events = append(events, event)
	})
	job, err := service.Create(Job{
		ID:             "job-1",
		Name:           "Once",
		Enabled:        true,
		Channel:        ChannelWeb,
		WorkspaceID:    "default",
		AgentID:        "claude",
		ConversationID: "conv-1",
		Prompt:         "hello",
		Schedule:       Schedule{Type: "once", RunAt: time.Now().Add(time.Hour).UnixMilli()},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.RunNow(job.ID); err != nil {
		t.Fatal(err)
	}
	updated, _ := service.Get(job.ID)
	if updated.Enabled {
		t.Fatal("once job should be disabled after run")
	}
	if updated.State.LastStatus != "success" || updated.State.RunCount != 1 || updated.ConversationID != "conv-1" {
		t.Fatalf("updated = %#v", updated)
	}
	var updateEvent *Event
	for i := range events {
		if events[i].Type == "job_updated" {
			updateEvent = &events[i]
		}
	}
	if updateEvent == nil || updateEvent.Channel != ChannelWeb || updateEvent.ConversationID != "conv-1" {
		t.Fatalf("job_updated event = %#v", updateEvent)
	}
}

func TestRunNowSkippedDoesNotIncrementRunCount(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "jobs.json"))
	service := NewService(store, testRunner{conversationID: "conv-1", err: SkippedError{Reason: "busy"}}, nil)
	job, err := service.Create(Job{
		ID:             "job-1",
		Name:           "Interval",
		Enabled:        true,
		WorkspaceID:    "default",
		AgentID:        "claude",
		ConversationID: "conv-1",
		Prompt:         "hello",
		Schedule:       Schedule{Type: "interval", EverySeconds: 60},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = service.RunNow(job.ID)
	updated, _ := service.Get(job.ID)
	if updated.State.LastStatus != "skipped" || updated.State.RunCount != 0 {
		t.Fatalf("updated = %#v", updated)
	}
}

func TestRunNowErrorIncrementsRunCount(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "jobs.json"))
	service := NewService(store, testRunner{conversationID: "conv-1", err: errors.New("failed")}, nil)
	job, err := service.Create(Job{
		ID:             "job-1",
		Name:           "Interval",
		Enabled:        true,
		WorkspaceID:    "default",
		AgentID:        "claude",
		ConversationID: "conv-1",
		Prompt:         "hello",
		Schedule:       Schedule{Type: "interval", EverySeconds: 60},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = service.RunNow(job.ID)
	updated, _ := service.Get(job.ID)
	if updated.State.LastStatus != "error" || updated.State.RunCount != 1 {
		t.Fatalf("updated = %#v", updated)
	}
}

func TestListFiltered(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "jobs.json"))
	service := NewService(store, testRunner{}, nil)
	for _, job := range []Job{
		{
			ID:             "web-1",
			Name:           "Web One",
			Channel:        ChannelWeb,
			WorkspaceID:    "default",
			AgentID:        "claude",
			ConversationID: "conv-1",
			Prompt:         "hello",
			Schedule:       Schedule{Type: "interval", EverySeconds: 60},
		},
		{
			ID:             "web-2",
			Name:           "Web Two",
			Channel:        ChannelWeb,
			WorkspaceID:    "default",
			AgentID:        "claude",
			ConversationID: "conv-2",
			Prompt:         "hello",
			Schedule:       Schedule{Type: "interval", EverySeconds: 60},
		},
		{
			ID:             "wechat-1",
			Name:           "WeChat One",
			Channel:        ChannelWeChat,
			WorkspaceID:    "default",
			AgentID:        "claude",
			ConversationID: "wx-1",
			Prompt:         "hello",
			Schedule:       Schedule{Type: "interval", EverySeconds: 60},
		},
	} {
		if _, err := service.Create(job); err != nil {
			t.Fatal(err)
		}
	}

	if got := service.ListFiltered("", ""); len(got) != 3 {
		t.Fatalf("ListFiltered(all) len = %d, want 3", len(got))
	}
	if got := service.ListFiltered(ChannelWeb, ""); len(got) != 2 {
		t.Fatalf("ListFiltered(web) len = %d, want 2", len(got))
	}
	got := service.ListFiltered(ChannelWeb, "conv-1")
	if len(got) != 1 || got[0].ID != "web-1" {
		t.Fatalf("ListFiltered(web, conv-1) = %#v, want web-1", got)
	}
}

func TestScheduledRunSkipsDeletedJob(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "jobs.json"))
	calls := make(chan Job, 1)
	service := NewService(store, testRunner{conversationID: "conv-1", calls: calls}, nil)
	job, err := service.Create(Job{
		ID:             "job-1",
		Name:           "Interval",
		Enabled:        true,
		WorkspaceID:    "default",
		AgentID:        "claude",
		ConversationID: "conv-1",
		Prompt:         "hello",
		Schedule:       Schedule{Type: "interval", EverySeconds: 60},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Delete(job.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := service.run(job.ID, true); err != nil {
		t.Fatal(err)
	}
	select {
	case called := <-calls:
		t.Fatalf("runner was called for deleted job: %#v", called)
	default:
	}
}

func TestCreateCronJobValidatesAndComputesNextRun(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "jobs.json"))
	calls := make(chan Job, 1)
	service := NewService(store, testRunner{conversationID: "conv-1", calls: calls}, nil)
	job, err := service.Create(Job{
		ID:             "job-cron",
		Name:           "Daily",
		Enabled:        true,
		WorkspaceID:    "default",
		AgentID:        "claude",
		ConversationID: "conv-1",
		Prompt:         "hello",
		Schedule:       Schedule{Type: ScheduleCron, CronExpr: "0 8 * * *"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if job.State.NextRunAt <= 0 {
		t.Fatalf("NextRunAt = %d, want positive", job.State.NextRunAt)
	}
	select {
	case called := <-calls:
		t.Fatalf("runner was called during create: %#v", called)
	default:
	}
}

func TestCreateRejectsInvalidCronExpression(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "jobs.json"))
	service := NewService(store, testRunner{}, nil)
	_, err := service.Create(Job{
		ID:             "job-cron",
		Name:           "Bad",
		WorkspaceID:    "default",
		AgentID:        "claude",
		ConversationID: "conv-1",
		Prompt:         "hello",
		Schedule:       Schedule{Type: ScheduleCron, CronExpr: "bad"},
	})
	if err == nil || !strings.Contains(err.Error(), "invalid cron expression") {
		t.Fatalf("Create() error = %v, want invalid cron expression", err)
	}
}

func TestUpdateCronExpressionMuteAndResume(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "jobs.json"))
	service := NewService(store, testRunner{}, nil)
	created, err := service.Create(Job{
		ID:             "job-cron",
		Name:           "Daily",
		Enabled:        true,
		WorkspaceID:    "default",
		AgentID:        "claude",
		ConversationID: "conv-1",
		Prompt:         "hello",
		Schedule:       Schedule{Type: ScheduleCron, CronExpr: "0 8 * * *"},
	})
	if err != nil {
		t.Fatal(err)
	}
	paused, err := service.Update(created.ID, func(job Job) (Job, error) {
		job.Enabled = false
		job.Mute = true
		job.Schedule = Schedule{Type: ScheduleCron, CronExpr: "0 9 * * 1"}
		return job, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if paused.Enabled || !paused.Mute || paused.State.NextRunAt != 0 {
		t.Fatalf("paused = %#v", paused)
	}
	resumed, err := service.Update(created.ID, func(job Job) (Job, error) {
		job.Enabled = true
		return job, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !resumed.Enabled || resumed.State.NextRunAt <= 0 || resumed.Schedule.CronExpr != "0 9 * * 1" {
		t.Fatalf("resumed = %#v", resumed)
	}
}

func TestExecJobAndPromptAreMutuallyExclusive(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "jobs.json"))
	service := NewService(store, testRunner{}, nil)
	_, err := service.Create(Job{
		ID:             "job-exec",
		Name:           "Exec",
		WorkspaceID:    "default",
		AgentID:        "claude",
		ConversationID: "conv-1",
		Prompt:         "hello",
		Exec:           "echo hello",
		Schedule:       Schedule{Type: ScheduleCron, CronExpr: "*/30 * * * *"},
	})
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("Create() error = %v, want mutually exclusive", err)
	}
}

func TestTimeoutMarksJobFailed(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "jobs.json"))
	timeout := 0
	service := NewService(store, testRunner{conversationID: "conv-1", calls: make(chan Job, 1)}, nil)
	job, err := service.Create(Job{
		ID:             "job-timeout",
		Name:           "Timeout",
		WorkspaceID:    "default",
		AgentID:        "claude",
		ConversationID: "conv-1",
		Prompt:         "hello",
		TimeoutMins:    &timeout,
		Schedule:       Schedule{Type: ScheduleInterval, EverySeconds: 60},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := ExecutionTimeout(job); got != 0 {
		t.Fatalf("ExecutionTimeout() = %v, want unlimited", got)
	}
}
