package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/pengmide/lumi/internal/conversation"
	lumicron "github.com/pengmide/lumi/internal/cron"
)

type cronJobRequest struct {
	Name           string            `json:"name"`
	Description    string            `json:"description"`
	Prompt         string            `json:"prompt"`
	Exec           string            `json:"exec"`
	AgentID        string            `json:"agentId"`
	WorkspaceID    string            `json:"workspaceId"`
	ConversationID string            `json:"conversationId"`
	Channel        string            `json:"channel"`
	Enabled        *bool             `json:"enabled,omitempty"`
	Schedule       lumicron.Schedule `json:"schedule"`
	Silent         *bool             `json:"silent,omitempty"`
	Mute           bool              `json:"mute,omitempty"`
	SessionMode    string            `json:"sessionMode,omitempty"`
	WorkDir        string            `json:"workDir,omitempty"`
	Mode           string            `json:"mode,omitempty"`
	TimeoutMins    *int              `json:"timeoutMins,omitempty"`
	Target         lumicron.Target   `json:"target,omitempty"`
}

type cronJobUpdateRequest struct {
	Name           *string            `json:"name"`
	Description    *string            `json:"description"`
	Prompt         *string            `json:"prompt"`
	Exec           *string            `json:"exec"`
	AgentID        *string            `json:"agentId"`
	WorkspaceID    *string            `json:"workspaceId"`
	ConversationID *string            `json:"conversationId"`
	Channel        *string            `json:"channel"`
	Enabled        *bool              `json:"enabled"`
	Schedule       *lumicron.Schedule `json:"schedule"`
	Silent         *bool              `json:"silent,omitempty"`
	Mute           *bool              `json:"mute,omitempty"`
	SessionMode    *string            `json:"sessionMode,omitempty"`
	WorkDir        *string            `json:"workDir,omitempty"`
	Mode           *string            `json:"mode,omitempty"`
	TimeoutMins    *int               `json:"timeoutMins,omitempty"`
}

func (s *Server) handleCronJobs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		channel := strings.TrimSpace(r.URL.Query().Get("channel"))
		conversationID := strings.TrimSpace(r.URL.Query().Get("conversationId"))
		writeJSON(w, map[string]any{"jobs": s.cron.ListFiltered(channel, conversationID)})
	case "POST":
		var req cronJobRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, "Invalid request", http.StatusBadRequest)
			return
		}
		enabled := true
		if req.Enabled != nil {
			enabled = *req.Enabled
		}
		job := lumicron.Job{
			ID:             "cron_" + generateUUID(),
			Name:           strings.TrimSpace(req.Name),
			Description:    strings.TrimSpace(req.Description),
			Prompt:         strings.TrimSpace(req.Prompt),
			Exec:           strings.TrimSpace(req.Exec),
			AgentID:        strings.TrimSpace(req.AgentID),
			WorkspaceID:    strings.TrimSpace(req.WorkspaceID),
			Channel:        strings.TrimSpace(req.Channel),
			ConversationID: strings.TrimSpace(req.ConversationID),
			Enabled:        enabled,
			Schedule:       req.Schedule,
			Silent:         req.Silent,
			Mute:           req.Mute,
			SessionMode:    req.SessionMode,
			WorkDir:        req.WorkDir,
			Mode:           req.Mode,
			TimeoutMins:    req.TimeoutMins,
			Target:         req.Target,
		}
		created, err := s.cron.Create(job)
		if err != nil {
			writeError(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]any{"job": created})
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleCronJobByID(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/cron/jobs/")
	if path == "" {
		writeError(w, "Job ID required", http.StatusBadRequest)
		return
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	jobID := parts[0]

	if len(parts) == 2 && parts[1] == "run" {
		if r.Method != "POST" {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		scopeChannel, scopeConversationID := cronScopeFromQuery(r)
		if scopeConversationID == "" {
			writeError(w, "conversationId is required", http.StatusBadRequest)
			return
		}
		conversationID, err := s.cron.RunNowScoped(scopeChannel, scopeConversationID, jobID)
		if err != nil {
			writeError(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]any{"success": true, "conversationId": conversationID})
		return
	}

	switch r.Method {
	case "GET":
		scopeChannel, scopeConversationID := cronScopeFromQuery(r)
		if scopeConversationID == "" {
			writeError(w, "conversationId is required", http.StatusBadRequest)
			return
		}
		job, ok := s.cron.GetScoped(scopeChannel, scopeConversationID, jobID)
		if !ok {
			writeError(w, "Job not found", http.StatusNotFound)
			return
		}
		writeJSON(w, map[string]any{"job": job})
	case "PUT":
		var req cronJobUpdateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, "Invalid request", http.StatusBadRequest)
			return
		}
		scopeChannel, scopeConversationID := cronScopeFromQuery(r)
		if strings.TrimSpace(r.URL.Query().Get("channel")) == "" && req.Channel != nil {
			scopeChannel = strings.TrimSpace(*req.Channel)
		}
		if scopeConversationID == "" {
			if req.ConversationID != nil {
				scopeConversationID = strings.TrimSpace(*req.ConversationID)
			}
		}
		if scopeConversationID == "" {
			writeError(w, "conversationId is required", http.StatusBadRequest)
			return
		}
		updated, err := s.cron.UpdateScoped(scopeChannel, scopeConversationID, jobID, func(job lumicron.Job) (lumicron.Job, error) {
			if req.Name != nil && strings.TrimSpace(*req.Name) != "" {
				job.Name = strings.TrimSpace(*req.Name)
			}
			if req.Description != nil {
				job.Description = strings.TrimSpace(*req.Description)
			}
			if req.Prompt != nil && strings.TrimSpace(*req.Prompt) != "" {
				job.Prompt = strings.TrimSpace(*req.Prompt)
				job.Exec = ""
			}
			if req.Exec != nil {
				job.Exec = strings.TrimSpace(*req.Exec)
				if job.Exec != "" {
					job.Prompt = ""
				}
			}
			if req.AgentID != nil && strings.TrimSpace(*req.AgentID) != "" {
				job.AgentID = strings.TrimSpace(*req.AgentID)
			}
			if req.WorkspaceID != nil && strings.TrimSpace(*req.WorkspaceID) != "" {
				job.WorkspaceID = strings.TrimSpace(*req.WorkspaceID)
			}
			if req.Enabled != nil {
				job.Enabled = *req.Enabled
			}
			if req.Schedule != nil {
				job.Schedule = *req.Schedule
			}
			if req.Silent != nil {
				job.Silent = req.Silent
			}
			if req.Mute != nil {
				job.Mute = *req.Mute
			}
			if req.SessionMode != nil {
				job.SessionMode = strings.TrimSpace(*req.SessionMode)
			}
			if req.WorkDir != nil {
				job.WorkDir = strings.TrimSpace(*req.WorkDir)
			}
			if req.Mode != nil {
				job.Mode = strings.TrimSpace(*req.Mode)
			}
			if req.TimeoutMins != nil {
				job.TimeoutMins = req.TimeoutMins
			}
			return job, nil
		})
		if err != nil {
			writeError(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]any{"job": updated})
	case "DELETE":
		scopeChannel, scopeConversationID := cronScopeFromQuery(r)
		if scopeConversationID == "" {
			writeError(w, "conversationId is required", http.StatusBadRequest)
			return
		}
		if err := s.cron.DeleteScoped(scopeChannel, scopeConversationID, jobID); err != nil {
			writeError(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]any{"success": true})
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func cronScopeFromQuery(r *http.Request) (string, string) {
	channel := strings.TrimSpace(r.URL.Query().Get("channel"))
	if channel == "" {
		channel = lumicron.ChannelWeb
	}
	return channel, strings.TrimSpace(r.URL.Query().Get("conversationId"))
}

func (s *Server) handleCronEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	ch := make(chan lumicron.Event, 64)
	s.cronSubsMu.Lock()
	s.cronSubs[ch] = struct{}{}
	s.cronSubsMu.Unlock()
	defer func() {
		s.cronSubsMu.Lock()
		delete(s.cronSubs, ch)
		s.cronSubsMu.Unlock()
		close(ch)
	}()

	for {
		select {
		case event := <-ch:
			data, _ := json.Marshal(event)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, data)
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func (s *Server) broadcastCronEvent(event lumicron.Event) {
	if event.Channel != "" && event.Channel != lumicron.ChannelWeb {
		return
	}
	s.cronSubsMu.RLock()
	for ch := range s.cronSubs {
		select {
		case ch <- event:
		default:
		}
	}
	s.cronSubsMu.RUnlock()
}

func (s *Server) RunCronJob(job lumicron.Job) (string, error) {
	if job.Exec != "" {
		return s.runCronExecJob(job)
	}
	switch job.Channel {
	case lumicron.ChannelWeChat:
		return s.wechat.RunCronJob(context.Background(), job)
	case lumicron.ChannelWeCom:
		return s.wecom.RunCronJob(context.Background(), job)
	}
	convID := job.ConversationID
	if convID == "" {
		return "", errors.New("missing conversation binding")
	}
	if !s.conversations.Has(convID) {
		if stored, err := s.sessionStore.Load(convID); err == nil {
			s.restoreConversation(stored)
		} else {
			return convID, errors.New("conversation not found")
		}
	}

	if !s.acquireCronRun(convID) {
		return convID, lumicron.SkippedError{Reason: "conversation busy"}
	}
	defer s.releaseCronRun(convID)

	if !job.Mute && !isCronSilent(job) {
		triggeredAt := time.Now().UnixMilli()
		s.conversations.AddMessage(convID, conversation.Message{
			Role:    "assistant",
			Kind:    "cron_trigger",
			Content: fmt.Sprintf("Scheduled task %q triggered.", job.Name),
			Cron: &conversation.CronMessageMeta{
				JobID:       job.ID,
				JobName:     job.Name,
				TriggeredAt: triggeredAt,
			},
		})
		s.persistConversation(convID)
		s.broadcastCronEvent(lumicron.Event{
			Type:           "chat_event",
			Channel:        lumicron.ChannelWeb,
			ConversationID: convID,
			Event:          "cron_trigger",
			Data: map[string]any{
				"message": map[string]any{
					"role":    "assistant",
					"kind":    "cron_trigger",
					"content": fmt.Sprintf("Scheduled task %q triggered.", job.Name),
					"cron": map[string]any{
						"jobId":       job.ID,
						"jobName":     job.Name,
						"triggeredAt": triggeredAt,
					},
				},
			},
		})
	}

	req := chatRequest{
		Message:        job.Prompt,
		ConversationID: convID,
		WorkspaceID:    job.WorkspaceID,
		AgentID:        job.AgentID,
		Hidden:         true,
		CronJobID:      job.ID,
		CronJobName:    job.Name,
	}
	send := func(event string, data any) {
		if job.Mute {
			return
		}
		s.broadcastCronEvent(lumicron.Event{
			Type:           "chat_event",
			Channel:        lumicron.ChannelWeb,
			ConversationID: convID,
			Event:          event,
			Data:           data,
		})
	}
	prepared, err := s.prepareChat(context.Background(), req)
	if err != nil {
		send("error", map[string]string{"message": err.Error()})
		return convID, err
	}
	ctx := chatRuntimeContext{
		Request:   req,
		Prepared:  prepared,
		SendEvent: send,
		Context:   context.Background(),
		Result:    &chatRunResult{},
	}
	runtime, err := s.resolveWorkspaceRuntime(context.Background(), prepared.WorkspaceID, nil)
	if err != nil {
		send("error", runtimeErrorEventPayload(err))
		return convID, err
	}
	ctx.Prepared.WorkspacePath = runtime.WorkspacePath
	if runtime.Mode != "local" {
		ctx.Request.DeviceID = runtime.DeviceID
	}
	if ctx.Request.DeviceID == "" {
		s.handleLocalChat(ctx)
	} else {
		s.handleDeviceChat(ctx)
	}
	s.broadcastCronEvent(lumicron.Event{Type: "session_updated", Channel: lumicron.ChannelWeb, ConversationID: convID})
	if ctx.Result != nil && ctx.Result.Err != nil {
		return convID, ctx.Result.Err
	}
	return convID, nil
}

func (s *Server) runCronExecJob(job lumicron.Job) (string, error) {
	convID := job.ConversationID
	if convID == "" {
		return "", errors.New("missing conversation binding")
	}
	if !s.conversations.Has(convID) {
		if stored, err := s.sessionStore.Load(convID); err == nil {
			s.restoreConversation(stored)
		} else {
			return convID, errors.New("conversation not found")
		}
	}
	if !s.acquireCronRun(convID) {
		return convID, lumicron.SkippedError{Reason: "conversation busy"}
	}
	defer s.releaseCronRun(convID)

	ctx := context.Background()
	timeout := lumicron.ExecutionTimeout(job)
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, shellName(), shellFlag(), job.Exec)
	if job.WorkDir != "" {
		cmd.Dir = job.WorkDir
	} else if ws := s.resolveWorkspacePath(job.WorkspaceID); ws != "" {
		cmd.Dir = ws
	}
	output, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		err = errors.New("cron exec timed out")
	}
	if !job.Mute {
		content := strings.TrimSpace(string(output))
		if content == "" {
			content = "(no output)"
		}
		if err != nil {
			content = fmt.Sprintf("Scheduled task %q failed: %v\n\n%s", job.Name, err, content)
		}
		s.conversations.AddMessage(convID, conversation.Message{
			Role:    "assistant",
			Content: content,
			Cron: &conversation.CronMessageMeta{
				JobID:       job.ID,
				JobName:     job.Name,
				TriggeredAt: time.Now().UnixMilli(),
			},
		})
		s.persistConversation(convID)
		s.broadcastCronEvent(lumicron.Event{Type: "session_updated", Channel: lumicron.ChannelWeb, ConversationID: convID})
	}
	return convID, err
}

func isCronSilent(job lumicron.Job) bool {
	return job.Silent != nil && *job.Silent
}

func shellName() string {
	if runtime.GOOS == "windows" {
		return "cmd"
	}
	return "sh"
}

func shellFlag() string {
	if runtime.GOOS == "windows" {
		return "/C"
	}
	return "-c"
}

func (s *Server) apiBaseForAgent() string {
	if s == nil {
		return lumiAPIBaseForConfig(nil)
	}
	return lumiAPIBaseForConfig(s.config)
}

func (s *Server) acquireCronRun(conversationID string) bool {
	s.cronRunsMu.Lock()
	defer s.cronRunsMu.Unlock()
	if _, ok := s.cronRuns[conversationID]; ok {
		return false
	}
	s.cronRuns[conversationID] = struct{}{}
	return true
}

func (s *Server) releaseCronRun(conversationID string) {
	s.cronRunsMu.Lock()
	defer s.cronRunsMu.Unlock()
	delete(s.cronRuns, conversationID)
}
