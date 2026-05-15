package device

import (
	"encoding/json"

	"github.com/pengmide/lumi/internal/setupcheck"
)

type MessageType string

const WebSocketReadLimit = 8 << 20 // 8 MiB

const (
	MsgAck               MessageType = "ack"
	MsgError             MessageType = "error"
	MsgDeviceRegister    MessageType = "device.register"
	MsgDeviceHeartbeat   MessageType = "device.heartbeat"
	MsgDeviceStatus      MessageType = "device.status"
	MsgSetupStatus       MessageType = "setup.status"
	MsgSetupCheck        MessageType = "setup.check"
	MsgTaskExecute       MessageType = "task.execute"
	MsgTaskSession       MessageType = "task.session"
	MsgTaskEvent         MessageType = "task.event"
	MsgTaskDone          MessageType = "task.done"
	MsgTaskError         MessageType = "task.error"
	MsgTaskCancel        MessageType = "task.cancel"
	MsgPermissionRequest MessageType = "permission.request"
	MsgPermissionConfirm MessageType = "permission.confirm"
	MsgWorkspaceTree     MessageType = "workspace.tree"
	MsgWorkspaceFiles    MessageType = "workspace.files"
	MsgWorkspaceMeta     MessageType = "workspace.meta"
	MsgWorkspaceText     MessageType = "workspace.text"
	MsgWorkspaceBuffer   MessageType = "workspace.buffer"
	MsgWorkspaceChanges  MessageType = "workspace.changes"
	MsgWorkspaceDiff     MessageType = "workspace.diff"
	MsgWorkspaceUpload   MessageType = "workspace.upload"
	MsgWorkspaceCleanup  MessageType = "workspace.cleanup"
	MsgWorkspaceResponse MessageType = "workspace.response"
)

type Envelope struct {
	Type     MessageType     `json:"type"`
	ID       string          `json:"id"`
	DeviceID string          `json:"deviceId,omitempty"`
	TaskID   string          `json:"taskId,omitempty"`
	Payload  json.RawMessage `json:"payload,omitempty"`
}

type AckPayload struct {
	OK bool `json:"ok"`
}

type ErrorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type DeviceRegisterPayload struct {
	DeviceID       string            `json:"deviceId"`
	Name           string            `json:"name"`
	Hidden         bool              `json:"hidden,omitempty"`
	DefaultAgentID string            `json:"defaultAgentId,omitempty"`
	Agents         []DeviceAgentInfo `json:"agents,omitempty"`
	WorkspaceID    string            `json:"workspaceId,omitempty"`
	Version        string            `json:"version,omitempty"`
}

type DeviceHeartbeatPayload struct {
	RunningTaskIDs []string `json:"runningTaskIds,omitempty"`
}

type DeviceStatusPayload struct {
	Status string `json:"status"`
}

type SetupStatusPayload = setupcheck.SetupStatus

type TaskSessionPayload struct {
	SessionID string `json:"sessionId"`
}

type ACPNotification struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type TaskEventPayload struct {
	SessionID    string          `json:"sessionId,omitempty"`
	Notification ACPNotification `json:"notification"`
}

type TaskDonePayload struct {
	Result json.RawMessage `json:"result,omitempty"`
}

type TaskErrorPayload struct {
	Message string `json:"message"`
}

type PermissionRequestPayload struct {
	SessionID string             `json:"sessionId"`
	Options   []PermissionOption `json:"options"`
	ToolCall  PermissionToolCall `json:"toolCall"`
}

type PermissionOption struct {
	OptionID string `json:"optionId"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
}

type PermissionToolCall struct {
	ToolCallID string          `json:"toolCallId"`
	RawInput   json.RawMessage `json:"rawInput,omitempty"`
	Status     string          `json:"status,omitempty"`
	Title      string          `json:"title,omitempty"`
	Kind       string          `json:"kind,omitempty"`
}

type SetupCheckPayload struct{}

type TaskExecutePayload struct {
	ConversationID string         `json:"conversationId"`
	AgentID        string         `json:"agentId"`
	SessionID      string         `json:"sessionId,omitempty"`
	WorkspaceID    string         `json:"workspaceId,omitempty"`
	WorkspacePath  string         `json:"workspacePath"`
	Prompt         string         `json:"prompt"`
	Files          []TaskFileInfo `json:"files,omitempty"`
}

type TaskFileInfo struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Size int64  `json:"size,omitempty"`
}

type TaskCancelPayload struct {
	SessionID string `json:"sessionId,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

type PermissionConfirmPayload struct {
	ToolCallID string `json:"toolCallId"`
	OptionID   string `json:"optionId"`
}

type WorkspaceRequestPayload struct {
	WorkspacePath string                `json:"workspacePath"`
	Path          string                `json:"path,omitempty"`
	Query         string                `json:"query,omitempty"`
	Limit         int                   `json:"limit,omitempty"`
	Files         []WorkspaceUploadFile `json:"files,omitempty"`
}

type WorkspaceUploadFile struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

type WorkspaceResponsePayload struct {
	OK      bool            `json:"ok"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Error   *ErrorPayload   `json:"error,omitempty"`
}

func DecodePayload[T any](env Envelope) (T, error) {
	var out T
	if len(env.Payload) == 0 {
		return out, nil
	}
	err := json.Unmarshal(env.Payload, &out)
	return out, err
}

func NewEnvelope(typ MessageType, id, deviceID, taskID string, payload any) (Envelope, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return Envelope{}, err
	}
	return Envelope{
		Type:     typ,
		ID:       id,
		DeviceID: deviceID,
		TaskID:   taskID,
		Payload:  raw,
	}, nil
}

func AckEnvelope(id string) Envelope {
	env, _ := NewEnvelope(MsgAck, id, "", "", AckPayload{OK: true})
	return env
}

func ErrorEnvelope(id, code, message string) Envelope {
	env, _ := NewEnvelope(MsgError, id, "", "", ErrorPayload{Code: code, Message: message})
	return env
}
