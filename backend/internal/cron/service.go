package cron

import (
	"errors"
	"strings"
	"sync"
	"time"

	robfigcron "github.com/robfig/cron/v3"
)

const MinIntervalSeconds int64 = 60
const DefaultTimeoutMins = 30

const (
	ChannelWeb    = "web"
	ChannelWeChat = "wechat"
	ChannelWeCom  = "wecom"
)

const (
	ScheduleCron     = "cron"
	ScheduleOnce     = "once"
	ScheduleInterval = "interval"

	SessionModeReuse     = "reuse"
	SessionModeNewPerRun = "new_per_run"
)

type Runner interface {
	RunCronJob(job Job) (conversationID string, err error)
}

type Service struct {
	store   *Store
	runner  Runner
	emit    func(Event)
	mu      sync.Mutex
	jobs    map[string]Job
	cron    *robfigcron.Cron
	entries map[string]robfigcron.EntryID
	timers  map[string]*time.Timer
	started bool
}

func NewService(store *Store, runner Runner, emit func(Event)) *Service {
	if emit == nil {
		emit = func(Event) {}
	}
	return &Service{
		store:   store,
		runner:  runner,
		emit:    emit,
		jobs:    make(map[string]Job),
		cron:    robfigcron.New(),
		entries: make(map[string]robfigcron.EntryID),
		timers:  make(map[string]*time.Timer),
	}
}

func (s *Service) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return nil
	}
	jobs, err := s.store.List()
	if err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	for _, job := range jobs {
		job = normalizeJob(job)
		if job.Enabled {
			job.State.NextRunAt = nextRunAt(job.Schedule, now)
		}
		if job.Enabled && isOrphanScopedJob(job) {
			job.Enabled = false
			job.State.NextRunAt = 0
			job.State.LastStatus = "error"
			job.State.LastError = "missing conversation binding"
		}
		s.jobs[job.ID] = job
		if job.Enabled {
			s.scheduleLocked(job)
		}
	}
	s.cron.Start()
	s.started = true
	return s.persistLocked()
}

func (s *Service) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, timer := range s.timers {
		timer.Stop()
		delete(s.timers, id)
	}
	if s.cron != nil {
		ctx := s.cron.Stop()
		select {
		case <-ctx.Done():
		case <-time.After(5 * time.Second):
		}
		s.cron = robfigcron.New()
		s.entries = make(map[string]robfigcron.EntryID)
	}
	s.started = false
}

func (s *Service) List() []Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	jobs := make([]Job, 0, len(s.jobs))
	for _, job := range s.jobs {
		jobs = append(jobs, job)
	}
	return jobs
}

func (s *Service) ListByScope(channel, conversationID string) []Job {
	channel = normalizeChannel(channel)
	s.mu.Lock()
	defer s.mu.Unlock()
	jobs := make([]Job, 0)
	for _, job := range s.jobs {
		if job.Channel == channel && job.ConversationID == conversationID {
			jobs = append(jobs, job)
		}
	}
	return jobs
}

func (s *Service) ListFiltered(channel, conversationID string) []Job {
	channel = strings.TrimSpace(channel)
	conversationID = strings.TrimSpace(conversationID)
	if channel != "" {
		channel = normalizeChannel(channel)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	jobs := make([]Job, 0)
	for _, job := range s.jobs {
		if channel != "" && job.Channel != channel {
			continue
		}
		if conversationID != "" && job.ConversationID != conversationID {
			continue
		}
		jobs = append(jobs, job)
	}
	return jobs
}

func (s *Service) Get(id string) (Job, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[id]
	return job, ok
}

func (s *Service) GetScoped(channel, conversationID, id string) (Job, bool) {
	channel = normalizeChannel(channel)
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[id]
	if !ok || job.Channel != channel || job.ConversationID != conversationID {
		return Job{}, false
	}
	return job, true
}

func (s *Service) Create(job Job) (Job, error) {
	job = normalizeJob(job)
	if err := validate(job); err != nil {
		return Job{}, err
	}
	now := time.Now().UnixMilli()
	if job.CreatedAt == 0 {
		job.CreatedAt = now
	}
	job.UpdatedAt = now
	if job.Enabled {
		job.State.NextRunAt = nextRunAt(job.Schedule, now)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs[job.ID] = job
	if job.Enabled {
		s.scheduleLocked(job)
	}
	if err := s.persistLocked(); err != nil {
		return Job{}, err
	}
	s.emit(Event{Type: "job_created", Job: &job, Channel: job.Channel, ConversationID: job.ConversationID})
	return job, nil
}

func (s *Service) Update(id string, apply func(Job) (Job, error)) (Job, error) {
	s.mu.Lock()
	job, ok := s.jobs[id]
	if !ok {
		s.mu.Unlock()
		return Job{}, errors.New("job not found")
	}
	updated, err := apply(job)
	if err != nil {
		s.mu.Unlock()
		return Job{}, err
	}
	updated = normalizeJob(updated)
	if err := validate(updated); err != nil {
		s.mu.Unlock()
		return Job{}, err
	}
	updated.ID = id
	updated.CreatedAt = job.CreatedAt
	updated.UpdatedAt = time.Now().UnixMilli()
	if updated.Enabled {
		updated.State.NextRunAt = nextRunAt(updated.Schedule, time.Now().UnixMilli())
	} else {
		updated.State.NextRunAt = 0
	}
	s.jobs[id] = updated
	s.stopLocked(id)
	if updated.Enabled {
		s.scheduleLocked(updated)
	}
	err = s.persistLocked()
	s.mu.Unlock()
	if err != nil {
		return Job{}, err
	}
	s.emit(Event{Type: "job_updated", Job: &updated, Channel: updated.Channel, ConversationID: updated.ConversationID})
	return updated, nil
}

func (s *Service) UpdateScoped(channel, conversationID, id string, apply func(Job) (Job, error)) (Job, error) {
	channel = normalizeChannel(channel)
	return s.Update(id, func(job Job) (Job, error) {
		if job.Channel != channel || job.ConversationID != conversationID {
			return Job{}, errors.New("job not found")
		}
		return apply(job)
	})
}

func (s *Service) Delete(id string) error {
	s.mu.Lock()
	job, ok := s.jobs[id]
	if !ok {
		s.mu.Unlock()
		return nil
	}
	delete(s.jobs, id)
	s.stopLocked(id)
	err := s.persistLocked()
	s.mu.Unlock()
	if err != nil {
		return err
	}
	s.emit(Event{Type: "job_deleted", JobID: id, Channel: job.Channel, ConversationID: job.ConversationID})
	return nil
}

func (s *Service) DeleteScoped(channel, conversationID, id string) error {
	if _, ok := s.GetScoped(channel, conversationID, id); !ok {
		return nil
	}
	return s.Delete(id)
}

func (s *Service) DeleteByScope(channel, conversationID string) (int, error) {
	channel = normalizeChannel(channel)
	s.mu.Lock()
	ids := make([]string, 0)
	for id, job := range s.jobs {
		if job.Channel == channel && job.ConversationID == conversationID {
			ids = append(ids, id)
		}
	}
	for _, id := range ids {
		delete(s.jobs, id)
		s.stopLocked(id)
	}
	err := s.persistLocked()
	s.mu.Unlock()
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		s.emit(Event{Type: "job_deleted", JobID: id, Channel: channel, ConversationID: conversationID})
	}
	return len(ids), nil
}

func (s *Service) RunNow(id string) (string, error) {
	return s.run(id, false)
}

func (s *Service) RunNowScoped(channel, conversationID, id string) (string, error) {
	job, ok := s.GetScoped(channel, conversationID, id)
	if !ok {
		return "", errors.New("job not found")
	}
	return s.run(job.ID, false)
}

func (s *Service) scheduleLocked(job Job) {
	s.stopLocked(job.ID)
	if !job.Enabled {
		return
	}
	if job.Schedule.Type == ScheduleCron {
		entryID, err := s.cron.AddFunc(job.Schedule.CronExpr, func() {
			_, _ = s.run(job.ID, true)
		})
		if err == nil {
			s.entries[job.ID] = entryID
			if s.started {
				entries := s.cron.Entries()
				for _, entry := range entries {
					if entry.ID == entryID {
						job.State.NextRunAt = entry.Next.UnixMilli()
						s.jobs[job.ID] = job
						break
					}
				}
			}
		}
		return
	}
	delay := time.Until(time.UnixMilli(job.State.NextRunAt))
	if delay < 0 {
		delay = 0
	}
	s.timers[job.ID] = time.AfterFunc(delay, func() {
		_, _ = s.run(job.ID, true)
	})
}

func (s *Service) stopLocked(id string) {
	if timer := s.timers[id]; timer != nil {
		timer.Stop()
		delete(s.timers, id)
	}
	if entryID, ok := s.entries[id]; ok {
		s.cron.Remove(entryID)
		delete(s.entries, id)
	}
}

func (s *Service) run(id string, requireEnabled bool) (string, error) {
	s.mu.Lock()
	job, ok := s.jobs[id]
	if !ok || (requireEnabled && !job.Enabled) {
		s.mu.Unlock()
		return "", nil
	}
	s.mu.Unlock()

	startedAt := time.Now().UnixMilli()
	conversationID, err := s.runWithTimeout(job)

	s.mu.Lock()
	current, ok := s.jobs[job.ID]
	if !ok {
		s.mu.Unlock()
		return conversationID, err
	}
	current.State.LastRunAt = startedAt
	if err != nil {
		var skipped SkippedError
		if errors.As(err, &skipped) {
			current.State.LastStatus = "skipped"
		} else {
			current.State.RunCount++
			current.State.LastStatus = "error"
		}
		current.State.LastError = err.Error()
	} else {
		current.State.RunCount++
		current.State.LastStatus = "success"
		current.State.LastError = ""
		if conversationID != "" && current.ConversationID == "" {
			current.ConversationID = conversationID
		}
	}
	if current.Schedule.Type == ScheduleOnce {
		current.Enabled = false
		current.State.NextRunAt = 0
	} else if current.Enabled {
		current.State.NextRunAt = nextRunAt(current.Schedule, time.Now().UnixMilli())
	}
	s.jobs[current.ID] = current
	if current.Enabled {
		s.scheduleLocked(current)
	} else {
		s.stopLocked(current.ID)
	}
	persistErr := s.persistLocked()
	s.mu.Unlock()

	s.emit(Event{Type: "job_updated", Job: &current, Channel: current.Channel, ConversationID: current.ConversationID})
	if conversationID != "" {
		s.emit(Event{Type: "session_updated", Channel: current.Channel, ConversationID: conversationID})
	}
	if err != nil {
		return conversationID, err
	}
	return conversationID, persistErr
}

func (s *Service) runWithTimeout(job Job) (string, error) {
	if s.runner == nil {
		return "", errors.New("cron runner is not configured")
	}
	timeout := ExecutionTimeout(job)
	if timeout <= 0 {
		return s.runner.RunCronJob(job)
	}
	type result struct {
		conversationID string
		err            error
	}
	ch := make(chan result, 1)
	go func() {
		conversationID, err := s.runner.RunCronJob(job)
		ch <- result{conversationID: conversationID, err: err}
	}()
	select {
	case res := <-ch:
		return res.conversationID, res.err
	case <-time.After(timeout):
		return job.ConversationID, errors.New("cron job timed out")
	}
}

func (s *Service) persistLocked() error {
	jobs := make([]Job, 0, len(s.jobs))
	for _, job := range s.jobs {
		jobs = append(jobs, job)
	}
	return s.store.SaveAll(jobs)
}

func validate(job Job) error {
	if job.ID == "" || job.Name == "" || job.AgentID == "" || job.WorkspaceID == "" || job.Channel == "" || job.ConversationID == "" {
		return errors.New("missing required job fields")
	}
	if strings.TrimSpace(job.Prompt) == "" && strings.TrimSpace(job.Exec) == "" {
		return errors.New("prompt or exec is required")
	}
	if strings.TrimSpace(job.Prompt) != "" && strings.TrimSpace(job.Exec) != "" {
		return errors.New("prompt and exec are mutually exclusive")
	}
	if job.TimeoutMins != nil && *job.TimeoutMins < 0 {
		return errors.New("timeoutMins must be non-negative")
	}
	switch job.Schedule.Type {
	case ScheduleCron:
		if strings.TrimSpace(job.Schedule.CronExpr) == "" {
			return errors.New("cronExpr is required")
		}
		if _, err := robfigcron.ParseStandard(job.Schedule.CronExpr); err != nil {
			return errors.New("invalid cron expression: " + err.Error())
		}
	case ScheduleOnce:
		if job.Schedule.RunAt <= 0 {
			return errors.New("runAt is required")
		}
	case ScheduleInterval:
		if job.Schedule.EverySeconds < MinIntervalSeconds {
			return errors.New("everySeconds must be at least 60")
		}
	default:
		return errors.New("unsupported schedule type")
	}
	return nil
}

func normalizeJob(job Job) Job {
	job.Channel = normalizeChannel(job.Channel)
	job.Name = strings.TrimSpace(job.Name)
	job.Description = strings.TrimSpace(job.Description)
	job.AgentID = strings.TrimSpace(job.AgentID)
	job.WorkspaceID = strings.TrimSpace(job.WorkspaceID)
	job.ConversationID = strings.TrimSpace(job.ConversationID)
	job.Prompt = strings.TrimSpace(job.Prompt)
	job.Exec = strings.TrimSpace(job.Exec)
	job.WorkDir = strings.TrimSpace(job.WorkDir)
	job.Mode = strings.TrimSpace(job.Mode)
	job.Schedule.Type = strings.TrimSpace(job.Schedule.Type)
	job.Schedule.CronExpr = strings.TrimSpace(job.Schedule.CronExpr)
	job.SessionMode = NormalizeSessionMode(job.SessionMode)
	if job.Description == "" {
		job.Description = job.Name
	}
	return job
}

func NormalizeSessionMode(mode string) string {
	switch strings.TrimSpace(strings.ReplaceAll(mode, "-", "_")) {
	case "", SessionModeReuse:
		return SessionModeReuse
	case SessionModeNewPerRun:
		return SessionModeNewPerRun
	default:
		return strings.TrimSpace(strings.ReplaceAll(mode, "-", "_"))
	}
}

func ExecutionTimeout(job Job) time.Duration {
	if job.TimeoutMins == nil {
		return time.Duration(DefaultTimeoutMins) * time.Minute
	}
	if *job.TimeoutMins <= 0 {
		return 0
	}
	return time.Duration(*job.TimeoutMins) * time.Minute
}

func normalizeChannel(channel string) string {
	switch channel {
	case ChannelWeChat, ChannelWeCom:
		return channel
	default:
		return ChannelWeb
	}
}

func isOrphanScopedJob(job Job) bool {
	return job.ConversationID == ""
}

func nextRunAt(schedule Schedule, now int64) int64 {
	switch schedule.Type {
	case ScheduleCron:
		parsed, err := robfigcron.ParseStandard(schedule.CronExpr)
		if err != nil {
			return 0
		}
		return parsed.Next(time.UnixMilli(now)).UnixMilli()
	case ScheduleOnce:
		return schedule.RunAt
	case ScheduleInterval:
		next := now + schedule.EverySeconds*1000
		return next
	default:
		return 0
	}
}
