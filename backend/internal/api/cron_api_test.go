package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	lumicron "github.com/pengmide/lumi/internal/cron"
)

func TestCronAPICreatesCronAndExecJobs(t *testing.T) {
	server := newTestAPIServer(t)
	body := bytes.NewBufferString(`{
		"name":"Disk",
		"description":"Disk",
		"exec":"echo ok",
		"agentId":"claude",
		"workspaceId":"default",
		"conversationId":"conv-1",
		"channel":"web",
		"schedule":{"type":"cron","cronExpr":"*/30 * * * *"},
		"mute":true,
		"sessionMode":"new-per-run",
		"timeoutMins":5
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/cron/jobs", body)
	rec := httptest.NewRecorder()
	server.handleCronJobs(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var result struct {
		Job lumicron.Job `json:"job"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Job.Exec != "echo ok" || result.Job.Prompt != "" || !result.Job.Mute || result.Job.SessionMode != lumicron.SessionModeNewPerRun {
		t.Fatalf("job = %#v", result.Job)
	}
	if result.Job.State.NextRunAt <= 0 {
		t.Fatalf("NextRunAt = %d, want positive", result.Job.State.NextRunAt)
	}
}

func TestCronAPIRejectsInvalidCron(t *testing.T) {
	server := newTestAPIServer(t)
	body := bytes.NewBufferString(`{
		"name":"Bad",
		"prompt":"hello",
		"agentId":"claude",
		"workspaceId":"default",
		"conversationId":"conv-1",
		"schedule":{"type":"cron","cronExpr":"bad"}
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/cron/jobs", body)
	rec := httptest.NewRecorder()
	server.handleCronJobs(rec, req)

	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid cron expression") {
		t.Fatalf("status=%d body=%s, want invalid cron expression", rec.Code, rec.Body.String())
	}
}

func TestCronAPIUpdateRequiresScopedConversationAndPausesWithEnabled(t *testing.T) {
	server := newTestAPIServer(t)
	created, err := server.cron.Create(lumicron.Job{
		ID:             "cron-1",
		Name:           "Greeting",
		Prompt:         "hello",
		AgentID:        "claude",
		WorkspaceID:    "default",
		ConversationID: "conv-1",
		Channel:        "web",
		Enabled:        true,
		Schedule:       lumicron.Schedule{Type: lumicron.ScheduleCron, CronExpr: "*/30 * * * *"},
	})
	if err != nil {
		t.Fatal(err)
	}

	body := bytes.NewBufferString(`{"enabled":false}`)
	req := httptest.NewRequest(http.MethodPut, "/api/cron/jobs/"+created.ID+"?conversationId=conv-1", body)
	rec := httptest.NewRecorder()
	server.handleCronJobByID(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var result struct {
		Job lumicron.Job `json:"job"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Job.Enabled || result.Job.State.NextRunAt != 0 {
		t.Fatalf("job = %#v, want paused with no next run", result.Job)
	}
}

func TestCronAPIUpdateSupportsWeComScope(t *testing.T) {
	server := newTestAPIServer(t)
	created, err := server.cron.Create(lumicron.Job{
		ID:             "cron-wecom-1",
		Name:           "WeCom Greeting",
		Prompt:         "hello",
		AgentID:        "claude",
		WorkspaceID:    "default",
		ConversationID: "wecom-chat-1",
		Channel:        lumicron.ChannelWeCom,
		Enabled:        true,
		Schedule:       lumicron.Schedule{Type: lumicron.ScheduleCron, CronExpr: "*/30 * * * *"},
	})
	if err != nil {
		t.Fatal(err)
	}

	body := bytes.NewBufferString(`{"enabled":false}`)
	req := httptest.NewRequest(http.MethodPut, "/api/cron/jobs/"+created.ID+"?channel=wecom&conversationId=wecom-chat-1", body)
	rec := httptest.NewRecorder()
	server.handleCronJobByID(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var result struct {
		Job lumicron.Job `json:"job"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Job.Enabled || result.Job.Channel != lumicron.ChannelWeCom || result.Job.State.NextRunAt != 0 {
		t.Fatalf("job = %#v, want paused wecom job with no next run", result.Job)
	}
}

func TestCronAPIDeleteSupportsWeComScope(t *testing.T) {
	server := newTestAPIServer(t)
	created, err := server.cron.Create(lumicron.Job{
		ID:             "cron-wecom-1",
		Name:           "WeCom Greeting",
		Prompt:         "hello",
		AgentID:        "claude",
		WorkspaceID:    "default",
		ConversationID: "wecom-chat-1",
		Channel:        lumicron.ChannelWeCom,
		Enabled:        true,
		Schedule:       lumicron.Schedule{Type: lumicron.ScheduleCron, CronExpr: "*/30 * * * *"},
	})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/cron/jobs/"+created.ID+"?channel=wecom&conversationId=wecom-chat-1", nil)
	rec := httptest.NewRecorder()
	server.handleCronJobByID(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if _, ok := server.cron.GetScoped(lumicron.ChannelWeCom, "wecom-chat-1", created.ID); ok {
		t.Fatalf("wecom job still exists after delete")
	}
}
