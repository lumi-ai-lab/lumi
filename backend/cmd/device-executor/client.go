package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/pengmide/lumi/internal/device"
	"github.com/pengmide/lumi/internal/setupcheck"
	workspacepreview "github.com/pengmide/lumi/internal/workspace"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

const (
	heartbeatInterval = 15 * time.Second
	reconnectDelay    = 3 * time.Second
	defaultWorkspace  = "default"
)

type MessageType = device.MessageType
type Envelope = device.Envelope
type ErrorPayload = device.ErrorPayload
type DeviceAgentInfo = device.DeviceAgentInfo
type DeviceRegisterPayload = device.DeviceRegisterPayload
type DeviceHeartbeatPayload = device.DeviceHeartbeatPayload
type DeviceStatusPayload = device.DeviceStatusPayload
type SetupStatus = setupcheck.SetupStatus
type TaskFileInfo = device.TaskFileInfo
type TaskExecutePayload = device.TaskExecutePayload
type TaskSessionPayload = device.TaskSessionPayload
type ACPNotification = device.ACPNotification
type TaskEventPayload = device.TaskEventPayload
type TaskDonePayload = device.TaskDonePayload
type TaskErrorPayload = device.TaskErrorPayload
type TaskCancelPayload = device.TaskCancelPayload
type PermissionOption = device.PermissionOption
type PermissionToolCall = device.PermissionToolCall
type PermissionRequestPayload = device.PermissionRequestPayload
type PermissionConfirmPayload = device.PermissionConfirmPayload
type WorkspaceRequestPayload = device.WorkspaceRequestPayload
type WorkspaceResponsePayload = device.WorkspaceResponsePayload
type WorkspaceUploadFile = device.WorkspaceUploadFile

const (
	MsgAck               = device.MsgAck
	MsgError             = device.MsgError
	MsgDeviceRegister    = device.MsgDeviceRegister
	MsgDeviceHeartbeat   = device.MsgDeviceHeartbeat
	MsgDeviceStatus      = device.MsgDeviceStatus
	MsgSetupStatus       = device.MsgSetupStatus
	MsgSetupCheck        = device.MsgSetupCheck
	MsgTaskExecute       = device.MsgTaskExecute
	MsgTaskSession       = device.MsgTaskSession
	MsgTaskEvent         = device.MsgTaskEvent
	MsgTaskDone          = device.MsgTaskDone
	MsgTaskError         = device.MsgTaskError
	MsgTaskCancel        = device.MsgTaskCancel
	MsgPermissionRequest = device.MsgPermissionRequest
	MsgPermissionConfirm = device.MsgPermissionConfirm
	MsgWorkspaceTree     = device.MsgWorkspaceTree
	MsgWorkspaceFiles    = device.MsgWorkspaceFiles
	MsgWorkspaceMeta     = device.MsgWorkspaceMeta
	MsgWorkspaceText     = device.MsgWorkspaceText
	MsgWorkspaceBuffer   = device.MsgWorkspaceBuffer
	MsgWorkspaceChanges  = device.MsgWorkspaceChanges
	MsgWorkspaceDiff     = device.MsgWorkspaceDiff
	MsgWorkspaceUpload   = device.MsgWorkspaceUpload
	MsgWorkspaceCleanup  = device.MsgWorkspaceCleanup
	MsgWorkspaceResponse = device.MsgWorkspaceResponse
)

type Client struct {
	server string
	token  string
	cfg    *ExecutorConfig

	runner *Runner
	files  *workspacepreview.Service
	diffs  *workspacepreview.ChangesService

	connMu sync.RWMutex
	conn   *websocketConn

	pendingMu sync.Mutex
	pending   map[string]chan Envelope

	setupMu    sync.RWMutex
	setupReady bool
}

func NewClient(server, token string, cfg *ExecutorConfig) *Client {
	c := &Client{
		server:  server,
		token:   token,
		cfg:     cfg,
		pending: make(map[string]chan Envelope),
		files:   workspacepreview.NewService(),
		diffs:   workspacepreview.NewChangesService(),
	}
	c.runner = NewRunner(cfg, c)
	return c
}

func (c *Client) Run(ctx context.Context) error {
	defer c.runner.Shutdown()
	defer func() {
		if err := c.diffs.DisposeAll(); err != nil {
			log.Printf("failed to dispose workspace change state: %v", err)
		}
	}()

	for {
		err := c.runOnce(ctx)
		if err == nil || errors.Is(err, context.Canceled) {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		log.Printf("device-executor disconnected: %v", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(reconnectDelay):
		}
	}
}

func (c *Client) runOnce(ctx context.Context) error {
	conn, err := dialWebSocket(ctx, c.server, c.token)
	if err != nil {
		return err
	}
	defer conn.Close()
	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()

	c.connMu.Lock()
	c.conn = conn
	c.connMu.Unlock()
	defer func() {
		c.connMu.Lock()
		if c.conn == conn {
			c.conn = nil
		}
		c.connMu.Unlock()
		c.runner.AbortCurrentTask("connection lost")
		c.failPending(io.EOF)
		c.setSetupReady(false)
	}()

	if err := c.sendRegister(ctx); err != nil {
		return err
	}

	go c.heartbeatLoop(ctx, conn)

	if err := c.runSetupCheckAndReport(ctx); err != nil {
		log.Printf("setup check failed: %v", err)
	}

	for {
		env, err := conn.ReadEnvelope(ctx)
		if err != nil {
			return err
		}
		if c.resolvePending(env) {
			continue
		}
		if err := c.handleEnvelope(ctx, env); err != nil {
			log.Printf("failed to handle message %s: %v", env.Type, err)
		}
	}
}

func (c *Client) Send(msgType MessageType, taskID string, payload any) error {
	env, err := c.newEnvelope(msgType, taskID, payload)
	if err != nil {
		return err
	}
	return c.writeEnvelope(env)
}

func (c *Client) SetupReady() bool {
	c.setupMu.RLock()
	defer c.setupMu.RUnlock()
	return c.setupReady
}

func (c *Client) setSetupReady(ready bool) {
	c.setupMu.Lock()
	c.setupReady = ready
	c.setupMu.Unlock()
}

func (c *Client) sendRegister(ctx context.Context) error {
	agents := make([]DeviceAgentInfo, 0, len(c.cfg.Agents))
	for _, agentCfg := range c.cfg.Agents {
		agents = append(agents, DeviceAgentInfo{
			ID:   agentCfg.ID,
			Name: agentCfg.Name,
		})
	}

	workspaceID := c.cfg.WorkspaceID
	if strings.TrimSpace(workspaceID) == "" {
		workspaceID = defaultWorkspace
	}

	return c.Send(MsgDeviceRegister, "", DeviceRegisterPayload{
		DeviceID:       c.cfg.DeviceID,
		Name:           c.cfg.Name,
		Hidden:         c.cfg.Hidden,
		DefaultAgentID: c.cfg.DefaultAgent,
		Agents:         agents,
		WorkspaceID:    workspaceID,
		Version:        "device-executor-dev",
	})
}

func (c *Client) sendRequest(ctx context.Context, msgType MessageType, taskID string, payload any) (Envelope, error) {
	env, err := c.newEnvelope(msgType, taskID, payload)
	if err != nil {
		return Envelope{}, err
	}

	respCh := make(chan Envelope, 1)
	c.pendingMu.Lock()
	c.pending[env.ID] = respCh
	c.pendingMu.Unlock()
	defer func() {
		c.pendingMu.Lock()
		delete(c.pending, env.ID)
		c.pendingMu.Unlock()
	}()

	if err := c.writeEnvelope(env); err != nil {
		return Envelope{}, err
	}

	select {
	case <-ctx.Done():
		return Envelope{}, ctx.Err()
	case resp, ok := <-respCh:
		if !ok {
			return Envelope{}, io.EOF
		}
		if resp.Type == MsgError {
			var payload ErrorPayload
			_ = json.Unmarshal(resp.Payload, &payload)
			if payload.Message == "" {
				payload.Message = "request rejected"
			}
			return Envelope{}, errors.New(payload.Message)
		}
		return resp, nil
	}
}

func (c *Client) newEnvelope(msgType MessageType, taskID string, payload any) (Envelope, error) {
	return device.NewEnvelope(msgType, randomMessageID(), c.cfg.DeviceID, taskID, payload)
}

func (c *Client) writeEnvelope(env Envelope) error {
	c.connMu.RLock()
	conn := c.conn
	c.connMu.RUnlock()
	if conn == nil {
		return errors.New("websocket is not connected")
	}
	if err := conn.WriteJSON(env); err != nil {
		_ = conn.Close()
		return err
	}
	return nil
}

func (c *Client) heartbeatLoop(ctx context.Context, conn *websocketConn) {
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := c.Send(MsgDeviceHeartbeat, "", DeviceHeartbeatPayload{
				RunningTaskIDs: c.runner.RunningTaskIDs(),
			}); err != nil {
				if !errors.Is(err, io.EOF) {
					log.Printf("heartbeat failed: %v", err)
				}
				return
			}
			if conn != c.currentConn() {
				return
			}
		}
	}
}

func (c *Client) currentConn() *websocketConn {
	c.connMu.RLock()
	defer c.connMu.RUnlock()
	return c.conn
}

func (c *Client) handleEnvelope(ctx context.Context, env Envelope) error {
	switch env.Type {
	case MsgAck, MsgError:
		return nil
	case MsgSetupCheck:
		if err := c.ack(env); err != nil {
			return err
		}
		return c.runSetupCheckAndReport(ctx)
	case MsgTaskExecute:
		if err := c.ack(env); err != nil {
			return err
		}
		go c.runner.Execute(ctx, env)
	case MsgTaskCancel:
		if err := c.ack(env); err != nil {
			return err
		}
		c.runner.Cancel(ctx, env)
	case MsgPermissionConfirm:
		if err := c.ack(env); err != nil {
			return err
		}
		c.runner.ConfirmPermission(ctx, env)
	case MsgWorkspaceTree, MsgWorkspaceFiles, MsgWorkspaceMeta, MsgWorkspaceText, MsgWorkspaceBuffer, MsgWorkspaceChanges, MsgWorkspaceDiff, MsgWorkspaceUpload, MsgWorkspaceCleanup:
		go c.handleWorkspaceRequest(ctx, env)
	default:
		log.Printf("ignoring unsupported message type %s", env.Type)
	}
	return nil
}

func (c *Client) ack(env Envelope) error {
	if env.ID == "" {
		return nil
	}
	return c.writeEnvelope(device.AckEnvelope(env.ID))
}

func (c *Client) resolvePending(env Envelope) bool {
	if env.ID == "" {
		return false
	}
	c.pendingMu.Lock()
	ch, ok := c.pending[env.ID]
	c.pendingMu.Unlock()
	if !ok {
		return false
	}
	ch <- env
	return true
}

func (c *Client) failPending(_ error) {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
}

func (c *Client) runSetupCheckAndReport(ctx context.Context) error {
	status := runSetupCheck(c.cfg)
	c.setSetupReady(status.Ready)
	log.Printf("setup check completed: ready=%v environment=%d agents=%d acpPackages=%d", status.Ready, len(status.Environment), len(status.Agents), len(status.ACPPackages))
	if err := c.Send(MsgSetupStatus, "", status); err != nil {
		return err
	}
	log.Printf("setup status sent: ready=%v", status.Ready)
	deviceStatus := "setup_required"
	if status.Ready {
		deviceStatus = "online"
	}
	if err := c.Send(MsgDeviceStatus, "", DeviceStatusPayload{Status: deviceStatus}); err != nil {
		return err
	}
	log.Printf("device status sent: %s", deviceStatus)
	return nil
}

func runSetupCheck(cfg *ExecutorConfig) SetupStatus {
	return setupcheck.Check(cfg.Agents)
}

func decodePayload[T any](env Envelope) (T, error) {
	return device.DecodePayload[T](env)
}

func randomMessageID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}

type websocketConn struct {
	conn *websocket.Conn
}

func dialWebSocket(ctx context.Context, serverURL, token string) (*websocketConn, error) {
	endpoint, err := deviceWebSocketURL(serverURL)
	if err != nil {
		return nil, err
	}

	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+token)
	headers.Set("User-Agent", "device-executor")
	conn, _, err := websocket.Dial(ctx, endpoint.String(), &websocket.DialOptions{HTTPHeader: headers})
	if err != nil {
		return nil, err
	}
	conn.SetReadLimit(device.WebSocketReadLimit)
	return &websocketConn{conn: conn}, nil
}

func (c *websocketConn) ReadEnvelope(ctx context.Context) (Envelope, error) {
	var env Envelope
	if err := wsjson.Read(ctx, c.conn, &env); err != nil {
		return Envelope{}, err
	}
	return env, nil
}

func (c *websocketConn) WriteJSON(v any) error {
	writeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return wsjson.Write(writeCtx, c.conn, v)
}

func (c *websocketConn) Close() error {
	return c.conn.Close(websocket.StatusNormalClosure, "device-executor closing")
}

func deviceWebSocketURL(serverURL string) (*url.URL, error) {
	base, err := url.Parse(serverURL)
	if err != nil {
		return nil, fmt.Errorf("invalid server URL: %w", err)
	}
	if base.Scheme == "" {
		return nil, errors.New("server URL must include scheme")
	}
	switch base.Scheme {
	case "http":
		base.Scheme = "ws"
	case "https":
		base.Scheme = "wss"
	case "ws", "wss":
	default:
		return nil, fmt.Errorf("unsupported server scheme %q", base.Scheme)
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/api/devices/ws"
	base.RawQuery = ""
	base.Fragment = ""
	return base, nil
}
