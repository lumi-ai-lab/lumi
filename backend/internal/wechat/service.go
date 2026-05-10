package wechat

import (
	"context"
	"errors"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/pengmide/lumi/internal/config"
	lumicron "github.com/pengmide/lumi/internal/cron"
)

type Status struct {
	Running       bool   `json:"running"`
	Configured    bool   `json:"configured"`
	ConfigError   string `json:"configError,omitempty"`
	LastError     string `json:"lastError,omitempty"`
	LastSyncAt    int64  `json:"lastSyncAt,omitempty"`
	LastLoginAt   int64  `json:"lastLoginAt,omitempty"`
	LastMessageAt int64  `json:"lastMessageAt,omitempty"`
}

type Service struct {
	config       *config.Config
	runner       ChatRunner
	configStore  *ConfigStore
	runtimeStore *RuntimeStore
	convStore    *ConversationStore

	login *loginManager

	mu            sync.RWMutex
	running       bool
	monitorCancel context.CancelFunc
	monitorDone   chan struct{}
	locks         *conversationLocks
	typing        *typingManager
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
	svc.typing = newTypingManager(svc)
	svc.login = newLoginManager(svc)

	if state, err := svc.runtimeStore.Load(); err == nil && state.Running {
		state.Running = false
		_ = svc.runtimeStore.Save(state)
	}
	return svc
}

func (s *Service) AutoStartIfEnabled() {
	cfg, err := s.configStore.Load()
	if err != nil {
		log.Printf("wechat: failed to load config during auto-start: %v", err)
		return
	}
	if !cfg.Enabled {
		return
	}
	if err := s.Start(); err != nil {
		log.Printf("wechat: auto-start failed: %v", err)
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

	if err := s.updateRuntime(func(state *RuntimeState) {
		state.Running = true
		state.LastError = ""
	}); err != nil {
		s.running = false
		s.monitorCancel = nil
		s.monitorDone = nil
		cancel()
		return err
	}

	go s.runMonitorLoop(ctx, normalizeConfig(cfg), done)
	return nil
}

func (s *Service) Stop() error {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		s.typing.StopAll()
		return s.updateRuntime(func(state *RuntimeState) {
			state.Running = false
		})
	}
	cancel := s.monitorCancel
	done := s.monitorDone
	s.monitorCancel = nil
	s.monitorDone = nil
	s.running = false
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	s.typing.StopAll()
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
		Running:       s.IsRunning(),
		LastError:     state.LastError,
		LastSyncAt:    state.LastSyncAt,
		LastLoginAt:   state.LastLoginAt,
		LastMessageAt: state.LastMessageAt,
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
	target := job.Target.WeChat
	if target == nil || strings.TrimSpace(target.ConversationKey) == "" {
		return job.ConversationID, errors.New("wechat cron target is missing")
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

	unlock, ok := s.locks.TryLock(job.ConversationID)
	if !ok {
		return job.ConversationID, lumicron.SkippedError{Reason: "conversation busy"}
	}
	defer unlock()

	client := NewClient(cfg)
	sink := &gatewayEventSink{}
	runErr := s.runner.RunWeChatChat(ctx, ChatRunInput{
		Message:             job.Prompt,
		ConversationID:      job.ConversationID,
		WorkspaceID:         workspace.ID,
		WorkspacePath:       workspace.Path,
		AgentID:             job.AgentID,
		PromptPrefix:        wechatSourceInstruction,
		SessionModeOverride: deriveSessionMode(job.AgentID),
		NewSession:          job.SessionMode == lumicron.SessionModeNewPerRun,
		ConversationStore:   s.convStore,
		CronTarget:          job.Target,
	}, sink)
	if runErr != nil && ctx.Err() != nil {
		return job.ConversationID, runErr
	}

	finalText := sink.FinalText()
	if sink.lastError != "" && finalText == "" {
		return job.ConversationID, client.SendText(ctx, target.ConversationKey, sink.lastError, target.ContextToken)
	}
	parsed := ParseSendProtocol(finalText, workspace.Path)
	sentMedia := false
	failures := append([]string(nil), parsed.Failures...)
	for _, action := range parsed.Actions {
		if action.Caption != "" {
			if err := client.SendText(ctx, target.ConversationKey, action.Caption, target.ContextToken); err != nil {
				failures = append(failures, failureText(action.Path, err.Error()))
				continue
			}
		}
		if err := client.UploadAndSendMedia(ctx, target.ConversationKey, action, target.ContextToken); err != nil {
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
	return job.ConversationID, client.SendText(ctx, target.ConversationKey, visibleText, target.ContextToken)
}

func (s *Service) TestConnection(ctx context.Context) error {
	cfg, err := s.configStore.Load()
	if err != nil {
		return err
	}
	if err := s.validateConfigForRuntime(cfg); err != nil {
		return err
	}
	client := NewClient(cfg)
	_, err = client.GetUpdates(ctx, "")
	if err != nil && isExpectedGetUpdatesProbeTimeout(err) {
		return nil
	}
	return err
}

func (s *Service) SaveConfig(ctx context.Context, next Config) (SanitizedConfig, error) {
	_ = ctx
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
	if cfg.LoginMode != "qr" && cfg.LoginMode != "manual" {
		return errors.New("loginMode must be qr or manual")
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
	return nil
}

func (s *Service) validateConfigForRuntime(cfg Config) error {
	if err := s.validateConfigForSave(cfg); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.AccountID) == "" {
		return errors.New("accountId is required")
	}
	if strings.TrimSpace(cfg.BotToken) == "" {
		return errors.New("botToken is required")
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

func isExpectedGetUpdatesProbeTimeout(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}
