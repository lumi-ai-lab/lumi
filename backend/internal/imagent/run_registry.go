package imagent

import (
	"context"
	"strings"
	"sync"
)

type RunRegistry struct {
	mu   sync.Mutex
	next uint64
	runs map[string]registeredRun
}

type registeredRun struct {
	token  uint64
	cancel context.CancelFunc
}

func NewRunRegistry() *RunRegistry {
	return &RunRegistry{
		runs: make(map[string]registeredRun),
	}
}

func (r *RunRegistry) Register(conversationID string, cancel context.CancelFunc) uint64 {
	if r == nil || strings.TrimSpace(conversationID) == "" || cancel == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	r.next++
	token := r.next
	r.runs[conversationID] = registeredRun{token: token, cancel: cancel}
	return token
}

func (r *RunRegistry) Unregister(conversationID string, token uint64) {
	if r == nil || strings.TrimSpace(conversationID) == "" || token == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	run, ok := r.runs[conversationID]
	if ok && run.token == token {
		delete(r.runs, conversationID)
	}
}

func (r *RunRegistry) Stop(conversationID string) bool {
	if r == nil || strings.TrimSpace(conversationID) == "" {
		return false
	}
	r.mu.Lock()
	run, ok := r.runs[conversationID]
	if ok {
		delete(r.runs, conversationID)
	}
	r.mu.Unlock()
	if !ok {
		return false
	}
	run.cancel()
	return true
}

func IsStopCommand(text string) bool {
	return normalizeCommandText(text) == "/stop"
}
