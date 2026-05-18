package api

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/pengmide/lumi/internal/agent"
	"github.com/pengmide/lumi/internal/config"
	"github.com/pengmide/lumi/internal/conversation"
	lumicron "github.com/pengmide/lumi/internal/cron"
	"github.com/pengmide/lumi/internal/device"
	"github.com/pengmide/lumi/internal/router"
	"github.com/pengmide/lumi/internal/sandbox"
	"github.com/pengmide/lumi/internal/setupcheck"
	"github.com/pengmide/lumi/internal/skills"
	"github.com/pengmide/lumi/internal/storage"
	"github.com/pengmide/lumi/internal/wechat"
	"github.com/pengmide/lumi/internal/wecom"
	workspacepreview "github.com/pengmide/lumi/internal/workspace"
)

const outputRuleWidth = 50

// Server is the HTTP server
type Server struct {
	config         *config.Config
	agents         *agent.Manager
	router         *router.Router
	conversations  *conversation.Manager
	sessionStore   *storage.SessionStore
	shareStore     *storage.ShareStore
	workspaceStore *storage.WorkspaceStore
	workspaceSvc   *workspacepreview.Service
	workspaceDiffs *workspacepreview.ChangesService
	skills         *skills.Registry
	devices        *device.Registry
	sandbox        sandboxManager
	staticFS       fs.FS
	wechat         *wechat.Service
	wechatChat     *wechatChatRuntime
	wecom          *wecom.Service
	wecomChat      *wecomChatRuntime
	cron           *lumicron.Service
	cronSubs       map[chan lumicron.Event]struct{}
	cronSubsMu     sync.RWMutex
	cronRuns       map[string]struct{}
	cronRunsMu     sync.Mutex

	// Per-conversation agent sessions: convID -> agentID -> sessionID
	agentSessions map[string]map[string]string
	initialized   map[string]bool

	pendingPermissions   map[string]pendingPermissionState
	pendingPermissionsMu sync.RWMutex

	// conversationID -> deviceID -> agentID -> remote sessionID
	remoteAgentSessions map[string]map[string]map[string]string
	remoteSessionsMu    sync.RWMutex

	// Cached commands per agent
	agentCommands   map[string][]SlashCommand
	agentCommandsMu sync.RWMutex

	// Setup status cache
	setupStatus *setupcheck.SetupStatus
	setupMu     sync.RWMutex
	setupSubs   map[chan setupcheck.SetupStatus]struct{}
	setupSubsMu sync.RWMutex
}

type sandboxManager interface {
	Ensure(context.Context, sandbox.EnsureOptions) (sandbox.RuntimeState, *sandbox.RuntimeError)
	HasActiveRuntime() bool
	KeepAlive(config.WorkspaceConfig)
	Preflight(context.Context, sandbox.PreflightRequest) sandbox.PreflightResponse
	ShutdownPreserveContainers() error
	Status(config.WorkspaceConfig) sandbox.RuntimeState
	Terminate(context.Context, string) error
	Warmup(context.Context, sandbox.EnsureOptions) sandbox.RuntimeState
}

// NewServer creates a new HTTP server
func NewServer(cfg *config.Config, staticFS fs.FS) *Server {
	deviceStore := device.NewStore("")
	deviceSecret, err := device.EnsureSecret("")
	if err != nil {
		log.Fatalf("failed to initialize device secret: %v", err)
	}
	devices, err := device.NewRegistry(deviceStore, deviceSecret)
	if err != nil {
		log.Fatalf("failed to initialize device registry: %v", err)
	}
	sandboxManager, err := sandbox.NewManager(cfg, devices)
	if err != nil {
		log.Fatalf("failed to initialize sandbox manager: %v", err)
	}

	s := &Server{
		config:              cfg,
		agents:              agent.NewManager(cfg),
		router:              router.New(cfg),
		conversations:       conversation.NewManager(),
		sessionStore:        storage.NewSessionStore(""),
		shareStore:          storage.NewShareStore(""),
		workspaceStore:      storage.NewWorkspaceStore(""),
		workspaceSvc:        workspacepreview.NewService(),
		workspaceDiffs:      workspacepreview.NewChangesService(),
		skills:              skills.NewRegistry(),
		devices:             devices,
		sandbox:             sandboxManager,
		staticFS:            staticFS,
		agentSessions:       make(map[string]map[string]string),
		initialized:         make(map[string]bool),
		pendingPermissions:  make(map[string]pendingPermissionState),
		remoteAgentSessions: make(map[string]map[string]map[string]string),
		agentCommands:       make(map[string][]SlashCommand),
		setupSubs:           make(map[chan setupcheck.SetupStatus]struct{}),
		cronSubs:            make(map[chan lumicron.Event]struct{}),
		cronRuns:            make(map[string]struct{}),
	}
	s.cron = lumicron.NewService(lumicron.NewStore(""), s, s.broadcastCronEvent)
	s.wechatChat = newWeChatChatRuntime(cfg, s.cron)
	s.wechat = wechat.NewService(cfg, s)
	s.wecomChat = newWeComChatRuntime(cfg, s.cron)
	s.wecom = wecom.NewService(cfg, s)
	s.devices.SetDeviceResetHook(s.clearRemoteSessionsForDevice)

	s.loadPersistedWorkspaces()
	s.initSetupStatus()
	s.wechat.AutoStartIfEnabled()
	s.wecom.AutoStartIfEnabled()
	if err := s.cron.Start(); err != nil {
		log.Printf("failed to start cron service: %v", err)
	}
	go s.checkDependenciesAsync()
	return s
}

func (s *Server) loadPersistedWorkspaces() {
	persisted := s.workspaceStore.Load()
	for _, ws := range persisted {
		exists := false
		for _, existing := range s.config.Workspaces {
			if existing.ID == ws.ID {
				exists = true
				break
			}
		}
		if !exists {
			s.config.Workspaces = append(s.config.Workspaces, ws)
		}
	}
}

// Handler returns the HTTP handler
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// API routes
	mux.HandleFunc("/api/devices/ws", s.devices.HandleWebSocket)
	mux.HandleFunc("/api/devices/pairing-command", s.handleDevices)
	mux.HandleFunc("/api/devices", s.handleDevices)
	mux.HandleFunc("/api/devices/", s.handleDevices)
	mux.HandleFunc("/api/setup/status", s.handleSetupStatus)
	mux.HandleFunc("/api/setup/subscribe", s.handleSetupSubscribe)
	mux.HandleFunc("/api/setup/install", s.handleSetupInstall)
	mux.HandleFunc("/api/agents", s.handleAgents)
	mux.HandleFunc("/api/agents/update", s.handleAgentUpdate)
	mux.HandleFunc("/api/skills/presets", s.handleSkillPresets)
	mux.HandleFunc("/api/skills", s.handleSkills)
	mux.HandleFunc("/api/workspaces", s.handleWorkspaces)
	mux.HandleFunc("/api/workspaces/sandbox/preflight", s.handleSandboxPreflight)
	mux.HandleFunc("/api/workspaces/files", s.handleWorkspaceFiles)
	mux.HandleFunc("/api/workspaces/tree", s.handleWorkspaceTree)
	mux.HandleFunc("/api/workspaces/changes", s.handleWorkspaceChanges)
	mux.HandleFunc("/api/workspaces/diff", s.handleWorkspaceDiff)
	mux.HandleFunc("/api/workspaces/file", s.handleWorkspaceFile)
	mux.HandleFunc("/api/workspaces/file-buffer", s.handleWorkspaceFileBuffer)
	mux.HandleFunc("/api/workspaces/html-preview", s.handleWorkspaceHTMLPreview)
	mux.HandleFunc("/api/workspaces/html-asset/", s.handleWorkspaceHTMLAsset)
	mux.HandleFunc("/api/workspaces/meta", s.handleWorkspaceFileMeta)
	mux.HandleFunc("/api/shares/conversations", s.handleConversationShares)
	mux.HandleFunc("/api/shares/conversations/by-conversation/", s.handleConversationShareByConversation)
	mux.HandleFunc("/api/public/shares/conversations/", s.handlePublicConversationShares)
	mux.HandleFunc("/api/sessions", s.handleSessions)
	mux.HandleFunc("/api/sessions/new", s.handleSessionNew)
	mux.HandleFunc("/api/sessions/", s.handleSessionByID)
	mux.HandleFunc("/api/chat", s.handleChat)
	mux.HandleFunc("/api/chat/cancel", s.handleChatCancel)
	mux.HandleFunc("/api/cron/jobs", s.handleCronJobs)
	mux.HandleFunc("/api/cron/jobs/", s.handleCronJobByID)
	mux.HandleFunc("/api/cron/events", s.handleCronEvents)
	mux.HandleFunc("/api/permission/confirm", s.handlePermissionConfirm)
	mux.HandleFunc("/api/upload", s.handleFileUpload)
	mux.HandleFunc("/api/upload/cleanup", s.handleFileCleanup)
	mux.HandleFunc("/api/wechat/", s.wechat.HandleHTTP)
	mux.HandleFunc("/api/wecom/", s.wecom.HandleHTTP)
	mux.HandleFunc("/sandboxes/ensure", s.handleSandboxes)
	mux.HandleFunc("/sandboxes/warmup", s.handleSandboxes)
	mux.HandleFunc("/sandboxes/", s.handleSandboxes)

	// Static files
	if s.staticFS != nil {
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			path := strings.TrimPrefix(r.URL.Path, "/")
			resolvedPath := resolveStaticPath(path, s.staticFS)

			// Try to open the file
			f, err := s.staticFS.Open(resolvedPath)
			if err != nil {
				fallbackPath := resolveAppFallback(path)
				indexFile, err := s.staticFS.Open(fallbackPath)
				if err != nil {
					http.NotFound(w, r)
					return
				}
				defer indexFile.Close()

				stat, err := indexFile.Stat()
				if err != nil {
					http.NotFound(w, r)
					return
				}

				// Serve index.html with correct content type
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				http.ServeContent(w, r, fallbackPath, stat.ModTime(), indexFile.(io.ReadSeeker))
				return
			}
			f.Close()

			serveStaticFile(w, r, s.staticFS, resolvedPath)
		})
	}

	return recoveryMiddleware(corsMiddleware(mux))
}

// Shutdown stops all services while preserving sandbox containers for recovery.
func (s *Server) Shutdown() error {
	steps := []struct {
		name    string
		visible func() bool
		run     func() error
	}{
		{name: "Stopping WeChat service...", run: func() error {
			if s.wechat != nil {
				return s.wechat.Stop()
			}
			return nil
		}},
		{name: "Stopping WeChat agents...", run: func() error {
			if s.wechatChat != nil {
				return s.wechatChat.Shutdown()
			}
			return nil
		}},
		{name: "Stopping WeCom service...", run: func() error {
			if s.wecom != nil {
				return s.wecom.Stop()
			}
			return nil
		}},
		{name: "Stopping WeCom agents...", run: func() error {
			if s.wecomChat != nil {
				return s.wecomChat.Shutdown()
			}
			return nil
		}},
		{name: "Disposing workspace diff state...", run: func() error {
			if s.workspaceDiffs != nil {
				return s.workspaceDiffs.DisposeAll()
			}
			return nil
		}},
		{name: "Stopping sandbox manager (containers preserved)...", run: func() error {
			if s.sandbox != nil {
				return s.sandbox.ShutdownPreserveContainers()
			}
			return nil
		}, visible: func() bool {
			return s.sandbox != nil && s.sandbox.HasActiveRuntime()
		}},
		{name: "Stopping cron scheduler...", run: func() error {
			if s.cron != nil {
				s.cron.Stop()
			}
			return nil
		}},
		{name: "Stopping local agents...", run: func() error {
			if s.agents != nil {
				return s.agents.Shutdown()
			}
			return nil
		}},
	}

	fmt.Fprintln(os.Stderr, "\n⏳ Shutdown")
	fmt.Fprintln(os.Stderr, strings.Repeat("─", outputRuleWidth))
	for _, step := range steps {
		visible := step.visible == nil || step.visible()
		if visible {
			fmt.Fprintf(os.Stderr, "   %s\n", step.name)
		}
		if err := step.run(); err != nil {
			fmt.Fprintf(os.Stderr, "   %s failed: %v\n", step.name, err)
			return err
		}
	}
	fmt.Fprintln(os.Stderr, "   Shutdown complete.")
	return nil
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func recoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				log.Printf("PANIC recovered: %v\nPath: %s", err, r.URL.Path)
				http.Error(w, "Internal server error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// ListenAndServe starts the server
func (s *Server) ListenAndServe(addr string) error {
	if s.config != nil && strings.TrimSpace(s.config.PublicServerURL) == "" {
		host := "127.0.0.1"
		port := strings.TrimPrefix(addr, ":")
		if strings.Contains(addr, ":") && !strings.HasPrefix(addr, ":") {
			if h, p, err := net.SplitHostPort(addr); err == nil {
				if strings.TrimSpace(h) != "" {
					host = h
				}
				port = p
			}
		}
		if port != "" {
			s.config.PublicServerURL = "http://" + net.JoinHostPort(host, port)
		}
	}
	return http.ListenAndServe(addr, s.Handler())
}

func resolveStaticPath(path string, staticFS fs.FS) string {
	if path == "" {
		return "index.html"
	}

	candidates := []string{
		path,
		path + ".html",
		strings.TrimSuffix(path, "/") + "/index.html",
	}

	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		file, err := staticFS.Open(candidate)
		if err != nil {
			continue
		}
		stat, statErr := file.Stat()
		_ = file.Close()
		if statErr == nil && !stat.IsDir() {
			return candidate
		}
	}

	return path
}

func resolveAppFallback(path string) string {
	trimmed := strings.Trim(path, "/")
	switch {
	case trimmed == "", trimmed == "index.html":
		return "index.html"
	case trimmed == "c", strings.HasPrefix(trimmed, "c/"):
		return "c.html"
	case trimmed == "setup", strings.HasPrefix(trimmed, "setup/"):
		return "setup.html"
	default:
		return "index.html"
	}
}

func serveStaticFile(w http.ResponseWriter, r *http.Request, staticFS fs.FS, resolvedPath string) {
	data, err := fs.ReadFile(staticFS, resolvedPath)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	contentType := mime.TypeByExtension(fsPathExt(resolvedPath))
	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}

	modTime := time.Time{}
	if file, err := staticFS.Open(resolvedPath); err == nil {
		if stat, err := file.Stat(); err == nil {
			modTime = stat.ModTime()
		}
		_ = file.Close()
	}

	http.ServeContent(w, r, resolvedPath, modTime, bytes.NewReader(data))
}

func fsPathExt(path string) string {
	index := strings.LastIndex(path, ".")
	if index == -1 {
		return ""
	}
	return path[index:]
}

// StaticFS is embedded static files (set from main)
var StaticFS embed.FS
