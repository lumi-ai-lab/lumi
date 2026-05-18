import type {
  DeviceStatus,
  SandboxErrorCode,
  SandboxStage,
  SandboxStatus,
  Workspace,
} from "@/lib/types";

export interface WorkspaceStatusBadgeMeta {
  label: string;
  tone: string;
}

export interface SandboxStageStep {
  id: SandboxStage;
  label: string;
  status: "done" | "active" | "waiting";
}

export interface SandboxWorkspaceAlertState {
  tone: "info" | "warning" | "error";
  title: string;
  description: string;
  actionLabel: string;
  code?: SandboxErrorCode | null;
  details?: string;
  steps?: SandboxStageStep[];
}

const SANDBOX_STAGE_ORDER: SandboxStage[] = [
  "checking_docker",
  "preparing_image",
  "starting_container",
  "connecting_executor",
  "bootstrapping_runtime",
];

const SANDBOX_ERROR_CODES = new Set<SandboxErrorCode>([
  "ready",
  "path_invalid",
  "docker_unavailable",
  "docker_permission_denied",
  "image_missing",
  "image_pull_failed",
  "host_connect_unresolved",
  "executor_registration_timeout",
  "sandbox_unavailable",
  "unknown",
]);

const REMOTE_STATUS_TONES: Record<DeviceStatus, string> = {
  online: "bg-emerald-500/15 text-emerald-200",
  busy: "bg-amber-500/15 text-amber-200",
  offline: "bg-zinc-500/15 text-zinc-300",
  error: "bg-rose-500/15 text-rose-200",
  setup_required: "bg-sky-500/15 text-sky-200",
};

const SANDBOX_STATUS_TONES: Record<SandboxStatus, string> = {
  pending: "bg-sky-500/15 text-sky-200",
  running: "bg-emerald-500/15 text-emerald-200",
  failed: "bg-rose-500/15 text-rose-200",
  terminating: "bg-amber-500/15 text-amber-200",
  terminated: "bg-zinc-500/15 text-zinc-300",
};

export function isSandboxWorkspace(
  workspace?: Workspace | null,
): workspace is Workspace & { kind: "sandbox" } {
  return workspace?.kind === "sandbox";
}

export function isRemoteWorkspace(
  workspace?: Workspace | null,
): workspace is Workspace & { kind: "remote" } {
  return workspace?.kind === "remote";
}

export function getRemoteStatusLabel(status?: DeviceStatus | null) {
  if (!status) return "";
  return status.replace("_", " ");
}

export function getSandboxStatusLabel(status?: SandboxStatus | null) {
  switch (status) {
    case "running":
      return "running";
    case "failed":
      return "failed";
    case "terminating":
      return "stopping";
    case "terminated":
      return "stopped";
    case "pending":
    default:
      return "starting";
  }
}

export function getWorkspaceStatusBadgeMeta(
  workspace: Workspace,
  remoteStatus?: DeviceStatus | null,
): WorkspaceStatusBadgeMeta | null {
  if (isSandboxWorkspace(workspace)) {
    const status =
      workspace.sandboxStatus ||
      (workspace.sandboxReady ? "running" : "pending");
    return {
      label: getSandboxStatusLabel(status),
      tone: SANDBOX_STATUS_TONES[status],
    };
  }

  if (isRemoteWorkspace(workspace)) {
    const status = remoteStatus || workspace.deviceStatus;
    if (!status) return null;
    return {
      label: getRemoteStatusLabel(status),
      tone: REMOTE_STATUS_TONES[status],
    };
  }

  return null;
}

export function isWorkspaceInteractionBlocked(workspace?: Workspace | null) {
  if (!isSandboxWorkspace(workspace)) {
    return false;
  }

  return !(workspace.sandboxStatus === "running" && workspace.sandboxReady);
}

export function getSandboxStageLabel(stage?: SandboxStage | null) {
  switch (stage) {
    case "checking_docker":
      return "Checking Docker";
    case "preparing_image":
      return "Preparing image";
    case "starting_container":
      return "Starting container";
    case "connecting_executor":
      return "Connecting executor";
    case "bootstrapping_runtime":
      return "Preparing runtime";
    default:
      return "Preparing sandbox";
  }
}

export function getSandboxErrorDisplay(code?: string | null): {
  title: string;
  description: string;
  actionLabel: string;
} {
  switch (code) {
    case "path_invalid":
      return {
        title: "Sandbox path is invalid",
        description:
          "Use an absolute host path that Lumi can access.",
        actionLabel: "Review path",
      };
    case "docker_unavailable":
      return {
        title: "Cannot connect to Docker",
        description:
          "Start Docker Desktop, then retry the sandbox runtime.",
        actionLabel: "Recheck Docker",
      };
    case "docker_permission_denied":
      return {
        title: "Docker permission denied",
        description:
          "Lumi cannot access Docker on this machine. Check Docker permissions, then retry.",
        actionLabel: "Recheck Docker",
      };
    case "image_missing":
      return {
        title: "Sandbox image not found locally",
        description:
          "The runtime can still be saved, but the first startup may need to pull the image.",
        actionLabel: "Retry sandbox startup",
      };
    case "image_pull_failed":
      return {
        title: "Sandbox image pull failed",
        description:
          "Check the image name, network access, or registry login, then retry.",
        actionLabel: "Retry sandbox startup",
      };
    case "host_connect_unresolved":
      return {
        title: "Sandbox networking could not be configured",
        description:
          "Lumi could not resolve a host address for the sandbox to call back into. Check local network settings, then retry.",
        actionLabel: "Retry sandbox startup",
      };
    case "executor_registration_timeout":
      return {
        title: "Sandbox executor did not connect",
        description:
          "The sandbox started, but its executor did not register in time. Retry startup or rebuild the runtime.",
        actionLabel: "Retry sandbox startup",
      };
    case "sandbox_unavailable":
      return {
        title: "Sandbox runtime is unavailable",
        description:
          "This content belongs to a sandbox workspace, but the runtime could not be started.",
        actionLabel: "Retry later",
      };
    default:
      return {
        title: "Sandbox runtime is unavailable",
        description:
          "The sandbox runtime could not be reached. Retry startup after the environment is ready.",
        actionLabel: "Retry sandbox startup",
      };
  }
}

export function isSandboxErrorCode(
  code?: string | null,
): code is SandboxErrorCode {
  return Boolean(code && SANDBOX_ERROR_CODES.has(code as SandboxErrorCode));
}

export function getReadableSandboxErrorMessage(
  codeOrMessage?: string | null,
  fallback = "Sandbox runtime is unavailable.",
) {
  if (isSandboxErrorCode(codeOrMessage)) {
    return getSandboxErrorDisplay(codeOrMessage).description;
  }

  return codeOrMessage || fallback;
}

export function getSandboxWorkspaceAlert(
  workspace?: Workspace | null,
): SandboxWorkspaceAlertState | null {
  if (!isSandboxWorkspace(workspace)) {
    return null;
  }

  const status =
    workspace.sandboxStatus || (workspace.sandboxReady ? "running" : "pending");
  if (status === "running" && workspace.sandboxReady) {
    return null;
  }

  if (status === "failed") {
    const error = getSandboxErrorDisplay(workspace.sandboxError);
    return {
      tone: "error",
      title: error.title,
      description: error.description,
      actionLabel: error.actionLabel,
      code: workspace.sandboxError || "unknown",
    };
  }

  if (status === "terminating") {
    return {
      tone: "warning",
      title: "Sandbox runtime is stopping",
      description:
        "Workspace access is paused while the sandbox runtime shuts down.",
      actionLabel: "Refresh workspace",
      code: workspace.sandboxError || null,
    };
  }

  if (status === "terminated") {
    return {
      tone: "warning",
      title: "Sandbox runtime is stopped",
      description:
        "Retry workspace access to start the sandbox runtime again.",
      actionLabel: "Retry sandbox startup",
      code: workspace.sandboxError || null,
    };
  }

  const currentStage = workspace.sandboxStage;
  const activeIndex = currentStage
    ? SANDBOX_STAGE_ORDER.indexOf(currentStage)
    : -1;

  return {
    tone: workspace.sandboxError ? "warning" : "info",
    title: "Sandbox runtime is starting",
    description: workspace.sandboxError
      ? getSandboxErrorDisplay(workspace.sandboxError).description
      : currentStage
        ? `${getSandboxStageLabel(currentStage)} is in progress. Chat and file access will unlock when startup completes.`
        : "Preparing the sandbox runtime. Chat and file access will unlock when startup completes.",
    actionLabel: workspace.sandboxError
      ? getSandboxErrorDisplay(workspace.sandboxError).actionLabel
      : "Refresh workspace",
    code: workspace.sandboxError || null,
    steps: SANDBOX_STAGE_ORDER.map((stage, index) => ({
      id: stage,
      label: getSandboxStageLabel(stage),
      status:
        activeIndex === -1
          ? index === 0
            ? "active"
            : "waiting"
          : index < activeIndex
            ? "done"
            : index === activeIndex
              ? "active"
              : "waiting",
    })),
  };
}
