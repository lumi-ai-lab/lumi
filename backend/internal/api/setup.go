package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/pengmide/lumi/internal/setupcheck"
)

type DependencyItem = setupcheck.DependencyItem
type SetupStatus = setupcheck.SetupStatus

func normalizePackageName(packageSpec string) string {
	if packageSpec == "" {
		return ""
	}

	if strings.HasPrefix(packageSpec, "@") {
		slashIndex := strings.Index(packageSpec, "/")
		if slashIndex == -1 {
			return packageSpec
		}
		versionIndex := strings.Index(packageSpec[slashIndex+1:], "@")
		if versionIndex == -1 {
			return packageSpec
		}
		return packageSpec[:slashIndex+1+versionIndex]
	}

	versionIndex := strings.Index(packageSpec, "@")
	if versionIndex == -1 {
		return packageSpec
	}
	return packageSpec[:versionIndex]
}

// initSetupStatus initializes status with all checks in "checking" state
func (s *Server) initSetupStatus() {
	status := setupcheck.InitialStatus(s.config.Agents)
	s.setupMu.Lock()
	s.setupStatus = &status
	s.setupMu.Unlock()
}

// checkDependenciesAsync checks all dependencies asynchronously
func (s *Server) checkDependenciesAsync() {
	status := setupcheck.Check(s.config.Agents)
	s.setupMu.Lock()
	s.setupStatus = &status
	s.setupMu.Unlock()
	s.broadcastSetupStatus()
}

// broadcastSetupStatus sends current status to all subscribers
func (s *Server) broadcastSetupStatus() {
	s.setupMu.RLock()
	status := SetupStatus{
		Ready:       s.setupStatus.Ready,
		Environment: append([]DependencyItem{}, s.setupStatus.Environment...),
		Agents:      append([]DependencyItem{}, s.setupStatus.Agents...),
		ACPPackages: append([]DependencyItem{}, s.setupStatus.ACPPackages...),
	}
	s.setupMu.RUnlock()

	s.setupSubsMu.RLock()
	for ch := range s.setupSubs {
		select {
		case ch <- status:
		default:
		}
	}
	s.setupSubsMu.RUnlock()
}

func (s *Server) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.setupMu.RLock()
	status := s.setupStatus
	s.setupMu.RUnlock()

	writeJSON(w, status)
}

func (s *Server) handleSetupSubscribe(w http.ResponseWriter, r *http.Request) {
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

	ch := make(chan SetupStatus, 10)
	s.setupSubsMu.Lock()
	s.setupSubs[ch] = struct{}{}
	s.setupSubsMu.Unlock()

	defer func() {
		s.setupSubsMu.Lock()
		delete(s.setupSubs, ch)
		s.setupSubsMu.Unlock()
		close(ch)
	}()

	// Re-initialize and re-check dependencies on each subscribe
	s.initSetupStatus()
	go s.checkDependenciesAsync()

	// Send current status (checking state)
	s.setupMu.RLock()
	currentStatus := SetupStatus{
		Ready:       s.setupStatus.Ready,
		Environment: append([]DependencyItem{}, s.setupStatus.Environment...),
		Agents:      append([]DependencyItem{}, s.setupStatus.Agents...),
		ACPPackages: append([]DependencyItem{}, s.setupStatus.ACPPackages...),
	}
	s.setupMu.RUnlock()

	jsonData, _ := json.Marshal(currentStatus)
	fmt.Fprintf(w, "data: %s\n\n", jsonData)
	flusher.Flush()

	for {
		select {
		case status := <-ch:
			jsonData, _ := json.Marshal(status)
			fmt.Fprintf(w, "data: %s\n\n", jsonData)
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func (s *Server) handleSetupInstall(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
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

	sendEvent := func(eventType string, data any) {
		jsonData, _ := json.Marshal(data)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, jsonData)
		flusher.Flush()
	}

	// Check environment first
	current := setupcheck.Check(s.config.Agents)
	if !current.Ready && hasMissingEnvironment(current.Environment) {
		sendEvent("done", map[string]any{
			"success": false,
			"error":   "npm and npx are required. Please install Node.js first.",
		})
		return
	}

	result := setupcheck.InstallMissing(*s.setupStatus, func(event setupcheck.InstallEvent) {
		switch event.Type {
		case "agent":
			s.setupMu.Lock()
			s.setupStatus.Agents[event.Index].Status = event.Status
			s.setupStatus.Agents[event.Index].Message = event.Message
			s.setupMu.Unlock()
		case "acp":
			s.setupMu.Lock()
			s.setupStatus.ACPPackages[event.Index].Status = event.Status
			s.setupStatus.ACPPackages[event.Index].Message = event.Message
			s.setupMu.Unlock()
		}
		s.broadcastSetupStatus()

		sendEvent("progress", map[string]any{
			"index":   event.Index,
			"type":    event.Type,
			"status":  event.Status,
			"message": event.Message,
		})
	}, func(msg string) {
		sendEvent("log", map[string]any{
			"message": msg,
		})
	})

	s.setupMu.Lock()
	s.setupStatus.Environment = result.Environment
	s.setupStatus.Agents = result.Agents
	s.setupStatus.ACPPackages = result.ACPPackages
	s.setupStatus.Ready = result.Success
	s.setupMu.Unlock()
	s.broadcastSetupStatus()

	sendEvent("done", map[string]any{"success": result.Success})
}

func hasMissingEnvironment(items []DependencyItem) bool {
	for _, item := range items {
		if item.Status != "ready" {
			return true
		}
	}
	return false
}
