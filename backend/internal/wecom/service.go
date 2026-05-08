package wecom

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/pengmide/lumi/internal/config"
	lumicron "github.com/pengmide/lumi/internal/cron"
)

type Status struct {
	Running         bool   `json:"running"`
	Configured      bool   `json:"configured"`
	ConfigError     string `json:"configError,omitempty"`
	LastError       string `json:"lastError,omitempty"`
	LastConnectedAt int64  `json:"lastConnectedAt,omitempty"`
	LastMessageAt   int64  `json:"lastMessageAt,omitempty"`
}

type Service struct {
	config       *config.Config
	runner       ChatRunner
	configStore  *ConfigStore
	runtimeStore *RuntimeStore
	convStore    *ConversationStore

	mu            sync.RWMutex
	running       bool
	monitorCancel context.CancelFunc
	monitorDone   chan struct{}
	runtime       *wsRuntime
	locks         *conversationLocks
}

func NewService(cfg *config.Config, runner ChatRunner) *Service {
	svc := &Service{
		config:       cfg,
		runner:       runner,
		configStore:  NewConfigStore(),
		runtimeStore: NewRuntimeStore(),
		convStore:    NewConversationStore(),
		locks:        newConversationLocks(),
	}
	if state, err := svc.runtimeStore.Load(); err == nil && state.Running {
		state.Running = false
		_ = svc.runtimeStore.Save(state)
	}
	return svc
}

func (s *Service) AutoStartIfEnabled() {
	cfg, err := s.configStore.Load()
	if err != nil || !cfg.Enabled {
		return
	}
	if err := s.Start(); err != nil {
		_ = s.updateRuntime(func(state *RuntimeState) {
			state.LastError = err.Error()
			state.Running = false
		})
	}
}

func (s *Service) Start() error {
	cfg, err := s.configStore.Load()
	if err != nil {
		return err
	}
	if err := s.validateConfigForRuntime(cfg); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.running = true
	s.monitorCancel = cancel
	s.monitorDone = done
	s.runtime = nil
	if err := s.updateRuntime(func(state *RuntimeState) {
		state.Running = true
		state.LastError = ""
	}); err != nil {
		s.running = false
		s.monitorCancel = nil
		s.monitorDone = nil
		s.runtime = nil
		cancel()
		return err
	}

	go s.runWebSocketLoop(ctx, normalizeConfig(cfg), done)
	return nil
}

func (s *Service) Stop() error {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return s.updateRuntime(func(state *RuntimeState) {
			state.Running = false
		})
	}
	cancel := s.monitorCancel
	done := s.monitorDone
	s.monitorCancel = nil
	s.monitorDone = nil
	s.runtime = nil
	s.running = false
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
	return nil
}

func (s *Service) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.running
}

func (s *Service) setRuntime(rt *wsRuntime) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runtime = rt
}

func (s *Service) currentRuntime() *wsRuntime {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.runtime
}

func (s *Service) GetStatus() (Status, error) {
	cfg, err := s.configStore.Load()
	if err != nil {
		return Status{}, err
	}
	state, err := s.runtimeStore.Load()
	if err != nil {
		return Status{}, err
	}

	status := Status{
		Running:         s.IsRunning(),
		LastError:       state.LastError,
		LastConnectedAt: state.LastConnectedAt,
		LastMessageAt:   state.LastMessageAt,
	}
	if err := s.validateConfigForRuntime(cfg); err != nil {
		status.Configured = false
		status.ConfigError = err.Error()
		return status, nil
	}
	status.Configured = true
	return status, nil
}

func (s *Service) RunCronJob(ctx context.Context, job lumicron.Job) (string, error) {
	target := job.Target.WeCom
	if target == nil || strings.TrimSpace(target.ChatID) == "" {
		return job.ConversationID, errors.New("wecom cron target is missing")
	}
	cfg, err := s.configStore.Load()
	if err != nil {
		return job.ConversationID, err
	}
	cfg = normalizeConfig(cfg)
	workspace := s.config.FindWorkspace(job.WorkspaceID)
	if workspace == nil {
		return job.ConversationID, errors.New("workspace not found")
	}
	if workspace.Kind != "" && workspace.Kind != "local" {
		return job.ConversationID, errors.New("workspace must be local")
	}
	if s.config.FindAgent(job.AgentID) == nil {
		return job.ConversationID, errors.New("agent not found")
	}
	sender := s.currentRuntime()
	if sender == nil {
		return job.ConversationID, errors.New("wecom websocket is not connected")
	}
	unlock, ok := s.locks.TryLock(job.ConversationID)
	if !ok {
		return job.ConversationID, lumicron.SkippedError{Reason: "conversation busy"}
	}
	defer unlock()

	sink := &gatewayEventSink{}
	runErr := s.runner.RunWeComChat(ctx, ChatRunInput{
		Message:             job.Prompt,
		ConversationID:      job.ConversationID,
		WorkspaceID:         workspace.ID,
		WorkspacePath:       workspace.Path,
		AgentID:             job.AgentID,
		PromptPrefix:        wecomSourceInstruction,
		SessionModeOverride: deriveSessionMode(job.AgentID),
		ConversationStore:   s.convStore,
		CronTarget:          job.Target,
	}, sink)
	if runErr != nil && ctx.Err() != nil {
		return job.ConversationID, runErr
	}

	rctx := replyContext{ReqID: target.ReqID, ChatID: target.ChatID, ChatType: target.ChatType, UserID: target.UserID}
	finalText := sink.FinalText()
	if sink.lastError != "" && finalText == "" {
		return job.ConversationID, sender.Send(ctx, rctx, sink.lastError)
	}
	parsed := ParseSendProtocol(finalText, workspace.Path)
	sentMedia := false
	failures := append([]string(nil), parsed.Failures...)
	for _, action := range parsed.Actions {
		if action.Caption != "" {
			if err := sender.Send(ctx, rctx, action.Caption); err != nil {
				failures = append(failures, failureText(action.Path, err.Error()))
				continue
			}
		}
		if err := sender.SendMedia(ctx, rctx, action); err != nil {
			failures = append(failures, failureText(action.Path, err.Error()))
			continue
		}
		sentMedia = true
	}
	visibleText := parsed.VisibleText
	if len(failures) > 0 {
		failureTextBlock := strings.Join(failures, "\n")
		if visibleText == "" {
			visibleText = failureTextBlock
		} else {
			visibleText += "\n\n" + failureTextBlock
		}
	}
	if visibleText == "" && !sentMedia {
		visibleText = fallbackDoneText
	}
	if visibleText == "" {
		return job.ConversationID, nil
	}
	return job.ConversationID, sender.Send(ctx, rctx, visibleText)
}

func (s *Service) TestConnection(ctx context.Context) error {
	cfg, err := s.configStore.Load()
	if err != nil {
		return err
	}
	if err := s.validateConfigForRuntime(cfg); err != nil {
		return err
	}
	runtime := newWebSocketRuntime(s, normalizeConfig(cfg))
	return runtime.TestConnection(ctx)
}

func (s *Service) SaveConfig(_ context.Context, next Config) (SanitizedConfig, error) {
	next = normalizeConfig(next)
	if err := s.validateConfigForSave(next); err != nil {
		return SanitizedConfig{}, err
	}
	if err := s.configStore.Save(next); err != nil {
		return SanitizedConfig{}, err
	}
	return SanitizeConfig(next), nil
}

func (s *Service) Enable() error {
	cfg, err := s.configStore.Load()
	if err != nil {
		return err
	}
	if err := s.validateConfigForRuntime(cfg); err != nil {
		return err
	}

	cfg.Enabled = true
	if err := s.configStore.Save(cfg); err != nil {
		return err
	}
	if err := s.Start(); err != nil {
		cfg.Enabled = false
		_ = s.configStore.Save(cfg)
		return err
	}
	return nil
}

func (s *Service) Disable() error {
	cfg, err := s.configStore.Load()
	if err != nil {
		return err
	}
	if err := s.Stop(); err != nil {
		return err
	}
	cfg.Enabled = false
	return s.configStore.Save(cfg)
}

func (s *Service) validateConfigForSave(cfg Config) error {
	if cfg.Mode != defaultMode {
		return errors.New("mode must be websocket")
	}
	if strings.TrimSpace(cfg.WorkspaceID) == "" {
		return errors.New("workspaceId is required")
	}
	workspace := s.config.FindWorkspace(cfg.WorkspaceID)
	if workspace == nil {
		return errors.New("workspace not found")
	}
	if workspace.Kind != "" && workspace.Kind != "local" {
		return errors.New("workspace must be local")
	}
	if strings.TrimSpace(cfg.AgentID) == "" {
		return errors.New("agentId is required")
	}
	if s.config.FindAgent(cfg.AgentID) == nil {
		return errors.New("agent not found")
	}
	if cfg.ConnectTimeoutMs < 1000 {
		return errors.New("connectTimeoutMs must be at least 1000")
	}
	if cfg.HeartbeatIntervalMs < 5000 {
		return errors.New("heartbeatIntervalMs must be at least 5000")
	}
	if cfg.MessageAckTimeoutMs < 1000 {
		return errors.New("messageAckTimeoutMs must be at least 1000")
	}
	return nil
}

func (s *Service) validateConfigForRuntime(cfg Config) error {
	if err := s.validateConfigForSave(cfg); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.BotID) == "" {
		return errors.New("botId is required")
	}
	if strings.TrimSpace(cfg.BotSecret) == "" {
		return errors.New("botSecret is required")
	}
	return nil
}

func (s *Service) updateRuntime(update func(*RuntimeState)) error {
	state, err := s.runtimeStore.Load()
	if err != nil {
		return err
	}
	update(&state)
	return s.runtimeStore.Save(state)
}

func sleepContext(ctx context.Context, wait time.Duration) error {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
