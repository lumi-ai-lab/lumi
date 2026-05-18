export interface SlashCommand {
  name: string;
  aliases?: string[];
  description: string;
  input?: {
    hint?: string;
  } | null;
  isSkill?: boolean;
}

export interface SkillInfo {
  name: string;
  aliases?: string[];
  displayName?: string;
  description?: string;
  source: string;
  skillFile: string;
}

export interface ProjectSkills {
  project: string;
  agentType: string;
  workspacePath: string;
  dirs: string[];
  skillCount: number;
  skills: SkillInfo[];
}

export interface SkillsResponse {
  projects: ProjectSkills[];
}

export interface AgentModeOption {
  value: string;
  label: string;
  description?: string;
}

export interface Agent {
  id: string;
  name: string;
  backend?: string;
  permissionMode?: "default" | "bypass" | string;
  sessionMode?: string;
  command?: string;
  args?: string[];
  commands?: SlashCommand[];
  env?: Record<string, string>;
  availableModes?: AgentModeOption[];
}

export interface Workspace {
  id: string;
  name: string;
  path: string;
  kind?: WorkspaceKind;
  image?: string;
  idleTimeoutSec?: number;
  agents?: string[];
  deviceId?: string;
  deviceName?: string;
  remotePath?: string;
  deviceStatus?: DeviceStatus;
  setupReady?: boolean;
  sandboxStatus?: SandboxStatus;
  sandboxStage?: SandboxStage;
  sandboxReady?: boolean;
  sandboxExpiresAt?: number;
  sandboxError?: SandboxErrorCode;
}

export type WorkspaceKind = "local" | "remote" | "sandbox";

export type SandboxStatus =
  | "pending"
  | "running"
  | "failed"
  | "terminating"
  | "terminated";

export type SandboxStage =
  | "checking_docker"
  | "preparing_image"
  | "starting_container"
  | "connecting_executor"
  | "bootstrapping_runtime";

export type SandboxErrorCode =
  | "ready"
  | "path_invalid"
  | "docker_unavailable"
  | "docker_permission_denied"
  | "image_missing"
  | "image_pull_failed"
  | "host_connect_unresolved"
  | "executor_registration_timeout"
  | "sandbox_unavailable"
  | "unknown";

export type DeviceStatus =
  | "setup_required"
  | "online"
  | "offline"
  | "busy"
  | "error";

export interface DeviceAgentInfo {
  id: string;
  name: string;
}

export interface SetupStatus {
  ready: boolean;
  environment: DependencyItem[];
  agents: DependencyItem[];
  acpPackages: DependencyItem[];
}

export interface WeChatConfig {
  enabled: boolean;
  loginMode: "qr" | "manual";
  accountId: string;
  baseUrl: string;
  workspaceId: string;
  agentId: string;
  hasToken: boolean;
  maskedToken?: string;
}

export interface WeChatStatus {
  running: boolean;
  configured: boolean;
  configError?: string;
  lastError?: string;
  lastSyncAt?: number;
  lastLoginAt?: number;
  lastMessageAt?: number;
}

export type WeChatLoginEvent =
  | { type: "qr"; ticket: string; imageUrl: string }
  | { type: "scanned" }
  | { type: "confirmed"; accountId: string; baseUrl: string; hasToken: boolean }
  | { type: "expired" }
  | { type: "error"; message: string }
  | { type: "done" };

export type SaveWeChatConfigInput = Omit<
  WeChatConfig,
  "hasToken" | "maskedToken"
> & {
  botToken?: string;
};

export interface WeComConfig {
  enabled: boolean;
  mode: "websocket" | string;
  botId: string;
  workspaceId: string;
  agentId: string;
  allowFrom: string;
  connectTimeoutMs: number;
  heartbeatIntervalMs: number;
  messageAckTimeoutMs: number;
  hasSecret: boolean;
  maskedSecret?: string;
}

export interface WeComStatus {
  running: boolean;
  configured: boolean;
  configError?: string;
  lastError?: string;
  lastConnectedAt?: number;
  lastMessageAt?: number;
}

export type SaveWeComConfigInput = Omit<
  WeComConfig,
  "hasSecret" | "maskedSecret"
> & {
  botSecret?: string;
};

export interface DeviceDTO {
  id: string;
  name: string;
  alias?: string;
  displayName: string;
  status: DeviceStatus;
  setupReady: boolean;
  setupStatus?: SetupStatus | null;
  defaultAgentId?: string;
  agents?: DeviceAgentInfo[];
  workspaceId?: string;
  version?: string;
  lastHeartbeat: number;
  registeredAt: number;
  updatedAt: number;
  runningTaskIds?: string[];
}

export interface SessionMeta {
  id: string;
  title: string;
  activeAgent: string;
  workspaceId?: string;
  messageCount: number;
  createdAt: number;
  updatedAt: number;
}

export type CronSchedule =
  | { type: "once"; runAt: number }
  | { type: "interval"; everySeconds: number }
  | { type: "cron"; cronExpr: string };

export interface CronJobState {
  lastRunAt?: number;
  nextRunAt?: number;
  lastStatus?: "success" | "error" | "skipped" | string;
  lastError?: string;
  runCount: number;
}

export interface CronTarget {
  wechat?: {
    conversationKey?: string;
    contextToken?: string;
  };
  wecom?: {
    reqId?: string;
    chatId?: string;
    chatType?: string;
    userId?: string;
  };
}

export interface CronJob {
  id: string;
  name: string;
  description?: string;
  enabled: boolean;
  channel?: "web" | "wechat" | "wecom" | string;
  workspaceId: string;
  agentId: string;
  conversationId?: string;
  schedule: CronSchedule;
  prompt?: string;
  exec?: string;
  silent?: boolean;
  mute?: boolean;
  sessionMode?: string;
  workDir?: string;
  mode?: string;
  timeoutMins?: number;
  target?: CronTarget;
  state: CronJobState;
  createdAt: number;
  updatedAt: number;
}

export type CronEvent =
  | { type: "job_created"; job: CronJob }
  | { type: "job_updated"; job: CronJob }
  | { type: "job_deleted"; jobId: string }
  | { type: "session_updated"; conversationId: string }
  | { type: "chat_event"; conversationId: string; event: string; data: Record<string, unknown> };

export interface MessageFile {
  name: string;
  path: string;
  size: number;
}

export interface ShareFile {
  path: string;
}

export interface ToolCall {
  toolCallId: string;
  toolName: string;
  kind?: string;
  title: string;
  description?: string;
  status: "pending" | "completed" | "error";
  input?: string;
  rawInput?: string;
  output?: string;
  error?: string;
}

export interface Message {
  role: "user" | "assistant";
  type?: "text" | "thinking";
  content: string;
  agent?: string;
  status?: "thinking" | "done";
  duration?: number;
  toolCall?: ToolCall;
  timestamp?: number;
  isError?: boolean;
  files?: MessageFile[];
  kind?: "text" | "cron_trigger";
  hidden?: boolean;
  cron?: { jobId: string; jobName: string; triggeredAt: number };
}

export interface Session {
  id: string;
  title: string;
  messages: Message[];
  activeAgent: string;
  workspaceId?: string;
  createdAt: number;
  updatedAt: number;
  pendingPermission?: PermissionRequest | null;
}

export interface ConversationShare {
  id: string;
  token: string;
  conversationId: string;
  files: ShareFile[];
  createdAt: number;
  updatedAt: number;
}

export interface PublicSharedConversation {
  id: string;
  title: string;
  files: MessageFile[];
  messages: Message[];
  createdAt: number;
  updatedAt: number;
}

export type StreamItem =
  | { type: "text"; data: string }
  | { type: "tool"; data: ToolCall }
  | { type: "thinking"; data: ThinkingData };

export interface ThinkingData {
  content: string;
  status: "thinking" | "done";
  duration?: number;
}

export interface SessionUpdate {
  sessionUpdate: string;
  content?: { type: string; text?: string };
  toolCallId?: string;
  title?: string;
  status?: string;
  kind?: string;
  rawInput?: Record<string, unknown>;
  error?: string;
  _meta?: {
    claudeCode?: {
      toolName?: string;
      toolResponse?: {
        stdout?: string;
        stderr?: string;
        type?: string;
        file?: { filePath: string; content: string };
      };
      error?: string;
    };
  };
}

export interface StreamEvent {
  conversationId?: string;
  agent?: string;
  sessionId?: string;
  isNew?: boolean;
  message?: string;
  update?: SessionUpdate;
  sessionUpdate?: string;
  stopReason?: string;
  error?: string;
}

export interface PermissionRequest {
  sessionId: string;
  conversationId?: string;
  agentId?: string;
  options: Array<{
    optionId: string;
    name: string;
    kind: "allow_once" | "allow_always" | "reject_once" | "reject_always";
  }>;
  toolCall: {
    toolCallId: string;
    rawInput?: Record<string, unknown>;
    status?: string;
    title?: string;
    kind?: string;
  };
}

export interface FileInfo {
  path: string;
  name: string;
  isDir: boolean;
}

export type WorkspacePreviewKind =
  | "code"
  | "markdown"
  | "image"
  | "pdf"
  | "html"
  | "unsupported";

export interface WorkspaceTreeEntry {
  path: string;
  name: string;
  isDir: boolean;
  previewKind?: WorkspacePreviewKind;
  children?: WorkspaceTreeEntry[];
  loaded?: boolean;
}

export interface WorkspaceFileMeta {
  path: string;
  name: string;
  size: number;
  modifiedAt: number;
  mime?: string;
  previewKind: WorkspacePreviewKind;
}

export interface WorkspaceTextFile {
  meta: WorkspaceFileMeta;
  content: string;
  truncated?: boolean;
}

export interface WorkspaceFileChange {
  path: string;
  status: "added" | "modified" | "deleted";
  insertions?: number;
  deletions?: number;
}

export interface WorkspaceFileDiff {
  path: string;
  content: string;
}

export interface UploadedFile {
  name: string;
  path: string;
  size: number;
}

export interface DependencyItem {
  name: string;
  command?: string;
  package?: string;
  status:
    | "checking"
    | "ready"
    | "missing"
    | "not_installed"
    | "installing"
    | "error"
    | "blocked";
  message?: string;
  install?: string;
}
