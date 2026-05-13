import type {
  Agent,
  ConversationShare,
  CronJob,
  CronSchedule,
  DeviceDTO,
  FileInfo,
  MessageFile,
  PublicSharedConversation,
  SandboxErrorCode,
  SaveWeComConfigInput,
  SaveWeChatConfigInput,
  ShareFile,
  Session,
  SessionMeta,
  SkillsResponse,
  StreamEvent,
  WeComConfig,
  WeComStatus,
  WeChatConfig,
  WeChatLoginEvent,
  WeChatStatus,
  Workspace,
  WorkspaceKind,
  WorkspaceFileChange,
  WorkspaceFileDiff,
  WorkspaceFileMeta,
  WorkspaceTextFile,
  WorkspaceTreeEntry,
  UploadedFile,
} from "@/lib/types";

const API_BASE = "/api";

async function readJson<T>(response: Response) {
  if (response.status === 204) return null as T;
  return response.json() as Promise<T>;
}

async function readApiError(response: Response) {
  try {
    const data = (await response.json()) as { error?: string };
    return data.error || "Request failed";
  } catch {
    return "Request failed";
  }
}

export interface CreateWorkspaceOptions {
  kind?: WorkspaceKind;
  image?: string;
  idleTimeoutSec?: number;
  agents?: string[];
  deviceId?: string;
  deviceName?: string;
  remotePath?: string;
}

export interface SandboxWorkspacePreflightResult {
  ok: boolean;
  code: SandboxErrorCode;
  message: string;
  recoverable: boolean;
  details: string;
}

export async function fetchAgents(): Promise<{
  agents: Agent[];
  default: string;
}> {
  const response = await fetch(`${API_BASE}/agents`);
  const data = await readJson<{
    agents?: Array<{
      id: string;
      name: string;
      backend?: string;
      permissionMode?: string;
      sessionMode?: string;
      command?: string;
      args?: string[];
      commands?: Agent["commands"];
      env?: Record<string, string>;
      availableModes?: Agent["availableModes"];
    }>;
    default: string;
  }>(response);

  const agents = (data.agents || []).map((agent) => ({
    id: agent.id,
    name: agent.name,
    backend: agent.backend,
    permissionMode: agent.permissionMode || "default",
    sessionMode: agent.sessionMode || "default",
    command: agent.command,
    args: agent.args,
    commands: agent.commands,
    env: agent.env || {},
    availableModes: agent.availableModes || [],
  }));

  return { agents, default: data.default };
}

export async function fetchWorkspaces(): Promise<{
  workspaces: Workspace[];
  default: string;
}> {
  const response = await fetch(`${API_BASE}/workspaces`);
  return readJson(response);
}

export async function fetchSkills(): Promise<SkillsResponse> {
  const response = await fetch(`${API_BASE}/skills`, {
    cache: "no-store",
  });
  if (!response.ok) {
    throw new Error(await readApiError(response));
  }

  const data = await readJson<Partial<SkillsResponse>>(response);
  return { projects: data.projects || [] };
}

export async function fetchDevices(): Promise<DeviceDTO[]> {
  const response = await fetch(`${API_BASE}/devices`, {
    cache: "no-store",
  });
  if (!response.ok) {
    throw new Error(await readApiError(response));
  }

  const data = await readJson<{ devices?: DeviceDTO[] }>(response);
  return data.devices || [];
}

export async function fetchDevicePairingCommand(): Promise<{
  command: string;
  server: string;
  configPath: string;
}> {
  const response = await fetch(`${API_BASE}/devices/pairing-command`, {
    cache: "no-store",
  });
  if (!response.ok) {
    throw new Error(await readApiError(response));
  }

  return readJson<{
    command: string;
    server: string;
    configPath: string;
  }>(response);
}

export async function updateDeviceAlias(deviceId: string, alias: string) {
  const response = await fetch(`${API_BASE}/devices/${encodeURIComponent(deviceId)}/alias`, {
    method: "PUT",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify({ alias }),
  });
  if (!response.ok) {
    throw new Error(await readApiError(response));
  }

  const data = await readJson<{ device: DeviceDTO }>(response);
  return data.device;
}

export async function requestDeviceSetupCheck(deviceId: string) {
  const response = await fetch(
    `${API_BASE}/devices/${encodeURIComponent(deviceId)}/setup/check`,
    {
      method: "POST",
    },
  );

  const data = await readJson<{ success?: boolean; message?: string; error?: string }>(
    response,
  );
  return response.ok
    ? { success: true as const, message: data.message }
    : {
        success: false as const,
        error: data.error || "Failed to request setup check",
      };
}

export async function fetchWorkspaceFiles(
  workspaceId: string,
  query = "",
  limit = 50,
): Promise<FileInfo[]> {
  const params = new URLSearchParams({
    workspaceId,
    q: query,
    limit: String(limit),
  });

  const response = await fetch(`${API_BASE}/workspaces/files?${params}`);
  const data = await readJson<{ files?: FileInfo[] }>(response);
  return data.files || [];
}

export async function fetchWorkspaceTree(
  workspaceId: string,
  path = "",
): Promise<WorkspaceTreeEntry[]> {
  const params = new URLSearchParams({ workspaceId });
  if (path) {
    params.set("path", path);
  }
  const response = await fetch(`${API_BASE}/workspaces/tree?${params}`, {
    cache: "no-store",
  });
  if (!response.ok) {
    throw new Error(await readApiError(response));
  }

  const data = await readJson<{ tree?: WorkspaceTreeEntry[] }>(response);
  return data.tree || [];
}

export async function fetchWorkspaceChanges(
  workspaceId: string,
): Promise<WorkspaceFileChange[]> {
  const params = new URLSearchParams({ workspaceId });
  const response = await fetch(`${API_BASE}/workspaces/changes?${params}`, {
    cache: "no-store",
  });
  if (!response.ok) {
    throw new Error(await readApiError(response));
  }

  const data = await readJson<{ changes?: WorkspaceFileChange[] }>(response);
  return data.changes || [];
}

export async function fetchWorkspaceDiff(
  workspaceId: string,
  path: string,
): Promise<WorkspaceFileDiff> {
  const params = new URLSearchParams({ workspaceId, path });
  const response = await fetch(`${API_BASE}/workspaces/diff?${params}`, {
    cache: "no-store",
  });
  if (!response.ok) {
    throw new Error(await readApiError(response));
  }

  return readJson<WorkspaceFileDiff>(response);
}

export async function fetchWorkspaceTextFile(
  workspaceId: string,
  path: string,
): Promise<WorkspaceTextFile> {
  const params = new URLSearchParams({ workspaceId, path });
  const response = await fetch(`${API_BASE}/workspaces/file?${params}`, {
    cache: "no-store",
  });
  if (!response.ok) {
    throw new Error(await readApiError(response));
  }

  return readJson<WorkspaceTextFile>(response);
}

export async function fetchWorkspaceFileMeta(
  workspaceId: string,
  path: string,
): Promise<WorkspaceFileMeta> {
  const params = new URLSearchParams({ workspaceId, path });
  const response = await fetch(`${API_BASE}/workspaces/meta?${params}`, {
    cache: "no-store",
  });
  if (!response.ok) {
    throw new Error(await readApiError(response));
  }

  const data = await readJson<{ meta: WorkspaceFileMeta }>(response);
  return data.meta;
}

export function buildWorkspaceFileBufferUrl(workspaceId: string, path: string) {
  const params = new URLSearchParams({ workspaceId, path });
  return `${API_BASE}/workspaces/file-buffer?${params}`;
}

export function buildWorkspaceHTMLPreviewUrl(
  workspaceId: string,
  path: string,
) {
  const params = new URLSearchParams({ workspaceId, path });
  return `${API_BASE}/workspaces/html-preview?${params}`;
}

export async function fetchConversationShare(
  conversationId: string,
): Promise<ConversationShare | null> {
  const response = await fetch(
    `${API_BASE}/shares/conversations/by-conversation/${encodeURIComponent(conversationId)}`,
    {
      cache: "no-store",
    },
  );
  if (!response.ok) {
    throw new Error(await readApiError(response));
  }

  const data = await readJson<{ share?: ConversationShare | null }>(response);
  return data.share || null;
}

export async function createConversationShare(
  conversationId: string,
  files: ShareFile[] = [],
): Promise<ConversationShare> {
  const response = await fetch(`${API_BASE}/shares/conversations`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify({ conversationId, files }),
  });
  if (!response.ok) {
    throw new Error(await readApiError(response));
  }

  const data = await readJson<{ share: ConversationShare }>(response);
  return data.share;
}

export async function revokeConversationShare(conversationId: string) {
  const response = await fetch(
    `${API_BASE}/shares/conversations/by-conversation/${encodeURIComponent(conversationId)}`,
    {
      method: "DELETE",
    },
  );
  if (!response.ok) {
    throw new Error(await readApiError(response));
  }
}

export function buildPublicShareUrl(token: string) {
  const encodedToken = encodeURIComponent(token);
  if (typeof window === "undefined") {
    return `/share?token=${encodedToken}`;
  }

  return new URL(`/share?token=${encodedToken}`, window.location.origin).toString();
}

export async function fetchPublicSharedConversation(
  token: string,
): Promise<PublicSharedConversation> {
  const response = await fetch(
    `${API_BASE}/public/shares/conversations/${encodeURIComponent(token)}`,
    {
      cache: "no-store",
    },
  );
  if (!response.ok) {
    throw new Error(await readApiError(response));
  }

  return readJson<PublicSharedConversation>(response);
}

export async function fetchPublicSharedFileMeta(
  token: string,
  fileId: string,
): Promise<WorkspaceFileMeta> {
  const params = new URLSearchParams({ fileId });
  const response = await fetch(
    `${API_BASE}/public/shares/conversations/${encodeURIComponent(token)}/file-meta?${params}`,
    {
      cache: "no-store",
    },
  );
  if (!response.ok) {
    throw new Error(await readApiError(response));
  }

  const data = await readJson<{ meta: WorkspaceFileMeta }>(response);
  return data.meta;
}

export async function fetchPublicSharedTextFile(
  token: string,
  fileId: string,
): Promise<WorkspaceTextFile> {
  const params = new URLSearchParams({ fileId });
  const response = await fetch(
    `${API_BASE}/public/shares/conversations/${encodeURIComponent(token)}/file-content?${params}`,
    {
      cache: "no-store",
    },
  );
  if (!response.ok) {
    throw new Error(await readApiError(response));
  }

  return readJson<WorkspaceTextFile>(response);
}

export function buildPublicSharedFileBufferUrl(token: string, fileId: string) {
  const params = new URLSearchParams({ fileId });
  return `${API_BASE}/public/shares/conversations/${encodeURIComponent(token)}/file-buffer?${params}`;
}

export function buildPublicSharedHTMLPreviewUrl(token: string, fileId: string) {
  const params = new URLSearchParams({ fileId });
  return `${API_BASE}/public/shares/conversations/${encodeURIComponent(token)}/html-preview?${params}`;
}

export async function createWorkspace(
  name: string,
  path: string,
  options?: CreateWorkspaceOptions,
) {
  const response = await fetch(`${API_BASE}/workspaces`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify({ name, path, ...options }),
  });

  const data = await readJson<{ workspace: Workspace; error?: string }>(
    response,
  );
  if (!response.ok) {
    return {
      workspace: null as Workspace | null,
      error: data.error || "Failed to create workspace",
    };
  }

  return {
    workspace: data.workspace,
    error: undefined,
  };
}

export async function preflightSandboxWorkspace(input: {
  path?: string;
  image?: string;
  checkImagePull?: boolean;
}): Promise<SandboxWorkspacePreflightResult> {
  const response = await fetch(`${API_BASE}/workspaces/sandbox/preflight`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify(input),
  });

  const data = await readJson<Partial<SandboxWorkspacePreflightResult> & { error?: string }>(
    response,
  );
  if (!response.ok) {
    throw new Error(data.error || data.message || "Failed to run sandbox preflight");
  }

  return {
    ok: Boolean(data.ok),
    code: data.code || "unknown",
    message: data.message || "",
    recoverable: data.recoverable ?? true,
    details: data.details || "",
  };
}

export async function fetchSessions(): Promise<SessionMeta[]> {
  const response = await fetch(`${API_BASE}/sessions`);
  const data = await readJson<{ sessions?: SessionMeta[] }>(response);
  return data.sessions || [];
}

export async function fetchSession(id: string): Promise<Session | null> {
  const response = await fetch(`${API_BASE}/sessions/${id}`);
  if (!response.ok) return null;

  const data = await readJson<{ session?: Session }>(response);
  return data.session || null;
}

export async function createSession(workspaceId?: string) {
  const response = await fetch(`${API_BASE}/sessions/new`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify({ workspaceId }),
  });

  const data = await readJson<{ session: SessionMeta }>(response);
  return data.session;
}

export async function deleteSession(id: string) {
  await fetch(`${API_BASE}/sessions/${id}`, {
    method: "DELETE",
  });
}

function cronScopeParams(conversationId: string) {
  return new URLSearchParams({ conversationId });
}

export async function fetchCronJobs(filters?: { channel?: string; conversationId?: string | null } | string | null): Promise<CronJob[]> {
  const params = new URLSearchParams();
  if (typeof filters === "string") {
    if (filters) params.set("conversationId", filters);
  } else if (filters) {
    if (filters.channel) params.set("channel", filters.channel);
    if (filters.conversationId) params.set("conversationId", filters.conversationId);
  }
  const query = params.toString();
  const response = await fetch(`${API_BASE}/cron/jobs${query ? `?${query}` : ""}`, { cache: "no-store" });
  if (!response.ok) {
    throw new Error(await readApiError(response));
  }
  const data = await readJson<{ jobs?: CronJob[] }>(response);
  return data.jobs || [];
}

export async function fetchCronJob(id: string, conversationId: string): Promise<CronJob | null> {
  const response = await fetch(`${API_BASE}/cron/jobs/${encodeURIComponent(id)}?${cronScopeParams(conversationId)}`, {
    cache: "no-store",
  });
  if (!response.ok) return null;
  const data = await readJson<{ job?: CronJob }>(response);
  return data.job || null;
}

export async function createCronJob(input: {
  name: string;
  description?: string;
  prompt?: string;
  exec?: string;
  agentId: string;
  workspaceId: string;
  conversationId?: string;
  channel?: string;
  schedule: CronSchedule;
  mute?: boolean;
  silent?: boolean;
  sessionMode?: string;
  workDir?: string;
  mode?: string;
  timeoutMins?: number;
}): Promise<CronJob> {
  const response = await fetch(`${API_BASE}/cron/jobs`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
  if (!response.ok) {
    throw new Error(await readApiError(response));
  }
  const data = await readJson<{ job: CronJob }>(response);
  return data.job;
}

export async function updateCronJob(
  id: string,
  input: Partial<{
    name: string;
    description: string;
    prompt: string;
    exec: string;
    agentId: string;
    workspaceId: string;
    conversationId: string;
    channel: string;
    enabled: boolean;
    schedule: CronSchedule;
    mute: boolean;
    silent: boolean;
    sessionMode: string;
    workDir: string;
    mode: string;
    timeoutMins: number;
  }>,
): Promise<CronJob> {
  if (!input.conversationId) {
    throw new Error("conversationId is required");
  }
  const response = await fetch(`${API_BASE}/cron/jobs/${encodeURIComponent(id)}?${cronScopeParams(input.conversationId)}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
  if (!response.ok) {
    throw new Error(await readApiError(response));
  }
  const data = await readJson<{ job: CronJob }>(response);
  return data.job;
}

export async function deleteCronJob(id: string, conversationId: string) {
  const response = await fetch(`${API_BASE}/cron/jobs/${encodeURIComponent(id)}?${cronScopeParams(conversationId)}`, {
    method: "DELETE",
  });
  if (!response.ok) {
    throw new Error(await readApiError(response));
  }
}

export async function runCronJobNow(id: string, conversationId: string): Promise<{ conversationId?: string }> {
  const response = await fetch(`${API_BASE}/cron/jobs/${encodeURIComponent(id)}/run?${cronScopeParams(conversationId)}`, {
    method: "POST",
  });
  if (!response.ok) {
    throw new Error(await readApiError(response));
  }
  return readJson<{ conversationId?: string }>(response);
}

export async function confirmPermission(
  agentId: string,
  toolCallId: string,
  optionId: string,
) {
  await fetch(`${API_BASE}/permission/confirm`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify({ agentId, toolCallId, optionId }),
  });
}

export async function updateAgentMode(
  agentId: string,
  sessionMode: string,
) {
  const response = await fetch(`${API_BASE}/agents/update`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify({ agentId, sessionMode }),
  });

  const data = await readJson<{ error?: string }>(response);
  return response.ok
    ? { success: true as const }
    : {
        success: false as const,
        error: data.error || "Failed to update agent",
      };
}

export async function updateAgentEnv(
  agentId: string,
  env: Record<string, string>,
) {
  const response = await fetch(`${API_BASE}/agents/update`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify({ agentId, env, updateEnv: true }),
  });

  const data = await readJson<{ error?: string }>(response);
  return response.ok
    ? { success: true as const }
    : { success: false as const, error: data.error || "Failed to update env" };
}

export async function cancelChat(agentId: string, sessionId: string) {
  const response = await fetch(`${API_BASE}/chat/cancel`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify({ agentId, sessionId }),
  });

  const data = await readJson<{ error?: string }>(response);
  return response.ok
    ? { success: true as const }
    : { success: false as const, error: data.error || "Failed to cancel" };
}

export async function uploadFiles(files: File[], workspaceId: string) {
  const formData = new FormData();
  formData.append("workspaceId", workspaceId);
  files.forEach((file) => formData.append("files", file));

  const response = await fetch(`${API_BASE}/upload`, {
    method: "POST",
    body: formData,
  });

  const data = await readJson<{ files?: UploadedFile[]; error?: string }>(
    response,
  );
  return response.ok
    ? { success: true as const, files: data.files }
    : { success: false as const, error: data.error || "Failed to upload" };
}

export async function fetchWeChatConfig(): Promise<WeChatConfig> {
  const response = await fetch(`${API_BASE}/wechat/config`, {
    cache: "no-store",
  });
  if (!response.ok) {
    throw new Error(await readApiError(response));
  }

  return readJson<WeChatConfig>(response);
}

export async function saveWeChatConfig(
  input: SaveWeChatConfigInput,
): Promise<WeChatConfig> {
  const response = await fetch(`${API_BASE}/wechat/config`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify(input),
  });
  if (!response.ok) {
    throw new Error(await readApiError(response));
  }

  const data = await readJson<{ success: boolean; config: WeChatConfig }>(
    response,
  );
  return data.config;
}

export async function fetchWeChatStatus(): Promise<WeChatStatus> {
  const response = await fetch(`${API_BASE}/wechat/status`, {
    cache: "no-store",
  });
  if (!response.ok) {
    throw new Error(await readApiError(response));
  }

  return readJson<WeChatStatus>(response);
}

export async function startWeChatLogin(): Promise<{ loginId: string }> {
  const response = await fetch(`${API_BASE}/wechat/login/start`, {
    method: "POST",
  });
  if (!response.ok) {
    throw new Error(await readApiError(response));
  }

  return readJson<{ loginId: string }>(response);
}

export function subscribeWeChatLogin(
  loginId: string,
  handlers: {
    onQR?: (event: Extract<WeChatLoginEvent, { type: "qr" }>) => void;
    onScanned?: () => void;
    onConfirmed?: (
      event: Extract<WeChatLoginEvent, { type: "confirmed" }>,
    ) => void;
    onExpired?: () => void;
    onError?: (event: Extract<WeChatLoginEvent, { type: "error" }>) => void;
    onDone?: () => void;
  },
) {
  const params = new URLSearchParams({ id: loginId });
  const eventSource = new EventSource(`${API_BASE}/wechat/login/events?${params}`);

  const qrListener = (event: Event) => {
    const message = event as MessageEvent<string>;
    const data = JSON.parse(message.data) as Omit<
      Extract<WeChatLoginEvent, { type: "qr" }>,
      "type"
    >;
    handlers.onQR?.({ type: "qr", ...data });
  };
  const scannedListener = () => {
    handlers.onScanned?.();
  };
  const confirmedListener = (event: Event) => {
    const message = event as MessageEvent<string>;
    const data = JSON.parse(message.data) as Omit<
      Extract<WeChatLoginEvent, { type: "confirmed" }>,
      "type"
    >;
    handlers.onConfirmed?.({ type: "confirmed", ...data });
  };
  const expiredListener = () => {
    handlers.onExpired?.();
  };
  const errorListener = (event: Event) => {
    const message = event as MessageEvent<string>;
    let payload: { message?: string } = {};
    try {
      payload = JSON.parse(message.data) as { message?: string };
    } catch {
      payload = { message: "Login failed" };
    }
    handlers.onError?.({ type: "error", message: payload.message || "Login failed" });
  };
  const doneListener = () => {
    handlers.onDone?.();
  };
  const transportErrorListener = () => {
    handlers.onError?.({ type: "error", message: "Login stream disconnected" });
  };

  eventSource.addEventListener("qr", qrListener);
  eventSource.addEventListener("scanned", scannedListener);
  eventSource.addEventListener("confirmed", confirmedListener);
  eventSource.addEventListener("expired", expiredListener);
  eventSource.addEventListener("error", errorListener);
  eventSource.addEventListener("done", doneListener);
  eventSource.onerror = transportErrorListener;

  return () => {
    eventSource.removeEventListener("qr", qrListener);
    eventSource.removeEventListener("scanned", scannedListener);
    eventSource.removeEventListener("confirmed", confirmedListener);
    eventSource.removeEventListener("expired", expiredListener);
    eventSource.removeEventListener("error", errorListener);
    eventSource.removeEventListener("done", doneListener);
    eventSource.close();
  };
}

export async function testWeChatConnection(): Promise<
  | { success: true; message?: string }
  | { success: false; error: string }
> {
  const response = await fetch(`${API_BASE}/wechat/test`, {
    method: "POST",
  });
  if (!response.ok) {
    throw new Error(await readApiError(response));
  }

  const data = await readJson<{ success: boolean; message?: string; error?: string }>(
    response,
  );
  return data.success
    ? { success: true, message: data.message }
    : { success: false, error: data.error || "Connection test failed" };
}

export async function enableWeChat() {
  const response = await fetch(`${API_BASE}/wechat/enable`, {
    method: "POST",
  });
  if (!response.ok) {
    throw new Error(await readApiError(response));
  }

  await readJson<{ success: boolean }>(response);
}

export async function disableWeChat() {
  const response = await fetch(`${API_BASE}/wechat/disable`, {
    method: "POST",
  });
  if (!response.ok) {
    throw new Error(await readApiError(response));
  }

  await readJson<{ success: boolean }>(response);
}

export async function fetchWeComConfig(): Promise<WeComConfig> {
  const response = await fetch(`${API_BASE}/wecom/config`, {
    cache: "no-store",
  });
  if (!response.ok) {
    throw new Error(await readApiError(response));
  }

  return readJson<WeComConfig>(response);
}

export async function saveWeComConfig(
  input: SaveWeComConfigInput,
): Promise<WeComConfig> {
  const response = await fetch(`${API_BASE}/wecom/config`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify(input),
  });
  if (!response.ok) {
    throw new Error(await readApiError(response));
  }

  const data = await readJson<{ success: boolean; config: WeComConfig }>(
    response,
  );
  return data.config;
}

export async function fetchWeComStatus(): Promise<WeComStatus> {
  const response = await fetch(`${API_BASE}/wecom/status`, {
    cache: "no-store",
  });
  if (!response.ok) {
    throw new Error(await readApiError(response));
  }

  return readJson<WeComStatus>(response);
}

export async function testWeComConnection(): Promise<
  | { success: true; message?: string }
  | { success: false; error: string }
> {
  const response = await fetch(`${API_BASE}/wecom/test`, {
    method: "POST",
  });
  if (!response.ok) {
    throw new Error(await readApiError(response));
  }

  const data = await readJson<{ success: boolean; message?: string; error?: string }>(
    response,
  );
  return data.success
    ? { success: true, message: data.message }
    : { success: false, error: data.error || "Connection test failed" };
}

export async function enableWeCom() {
  const response = await fetch(`${API_BASE}/wecom/enable`, {
    method: "POST",
  });
  if (!response.ok) {
    throw new Error(await readApiError(response));
  }

  await readJson<{ success: boolean }>(response);
}

export async function disableWeCom() {
  const response = await fetch(`${API_BASE}/wecom/disable`, {
    method: "POST",
  });
  if (!response.ok) {
    throw new Error(await readApiError(response));
  }

  await readJson<{ success: boolean }>(response);
}

export async function cleanupFiles(workspaceId: string) {
  const response = await fetch(`${API_BASE}/upload/cleanup`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify({ workspaceId }),
  });

  const data = await readJson<{ error?: string }>(response);
  return response.ok
    ? { success: true as const }
    : { success: false as const, error: data.error || "Failed to cleanup" };
}

export function sendMessage(
  message: string,
  conversationId: string | null,
  workspaceId: string | null,
  files: MessageFile[],
  onEvent: (event: unknown) => void,
  deviceId?: string,
) {
  const controller = new AbortController();

  fetch(`${API_BASE}/chat`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify({ message, conversationId, workspaceId, files, deviceId }),
    signal: controller.signal,
  })
    .then(async (response) => {
      const reader = response.body?.getReader();
      if (!reader) return;

      const decoder = new TextDecoder();
      let buffer = "";
      let currentEventType = "message";

      while (true) {
        const { done, value } = await reader.read();
        if (done) break;

        buffer += decoder.decode(value, { stream: true });
        const lines = buffer.split("\n");
        buffer = lines.pop() || "";

        for (const line of lines) {
          if (line.startsWith("event: ")) {
            currentEventType = line.slice(7).trim();
            continue;
          }

          if (!line.startsWith("data: ")) continue;

          try {
            const data = JSON.parse(line.slice(6)) as StreamEvent &
              Record<string, unknown>;
            if (currentEventType === "error") {
              onEvent({ error: data.message || "Unknown error", ...data });
            } else if (currentEventType === "commands") {
              onEvent({ _eventType: "commands", ...data });
            } else {
              onEvent({ _eventType: currentEventType, ...data });
            }
          } catch {
            // Ignore partial frames.
          }

          currentEventType = "message";
        }
      }
    })
    .catch((error) => {
      if (error.name !== "AbortError") {
        onEvent({ error: error.message });
      }
    });

  return controller;
}
