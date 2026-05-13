"use client";

import { type ReactNode, useEffect, useMemo, useState } from "react";
import {
  ChevronDown,
  ChevronRight,
  Eye,
  FileCode2,
  FileText,
  Folder,
  FolderOpen,
  ImageIcon,
  PanelRightClose,
  PanelRightOpen,
  RefreshCw,
  Search,
  X,
} from "lucide-react";

import { SandboxWorkspaceAlert } from "@/components/sandbox-workspace-alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { ScrollArea } from "@/components/ui/scroll-area";
import { useI18n } from "@/features/i18n/i18n-provider";
import type {
  PreviewKind,
  WorkspacePreviewDocument,
  WorkspaceTreeNode,
} from "@/features/workspace-preview/types";
import { useWorkspacePreviewModel } from "@/features/workspace-preview/use-workspace-preview-model";
import { CodePreview } from "@/features/workspace-preview/viewers/code-preview";
import { MarkdownPreview } from "@/features/workspace-preview/viewers/markdown-preview";
import { isWorkspaceInteractionBlocked } from "@/lib/sandbox";
import type { Workspace } from "@/lib/types";
import { cn } from "@/lib/utils";

function clamp(value: number, min: number, max: number) {
  return Math.min(max, Math.max(min, value));
}

function useCompactLayout() {
  const [isCompact, setIsCompact] = useState(false);

  useEffect(() => {
    const update = () => {
      setIsCompact(window.innerWidth < 1180);
    };

    update();
    window.addEventListener("resize", update);
    return () => window.removeEventListener("resize", update);
  }, []);

  return isCompact;
}

function useResizableWidth(
  initialWidth: number,
  minWidth: number,
  maxWidth: number,
) {
  const [width, setWidth] = useState(initialWidth);

  const startResize = (event: React.MouseEvent<HTMLDivElement>) => {
    event.preventDefault();
    const startX = event.clientX;
    const startWidth = width;

    const handleMove = (moveEvent: MouseEvent) => {
      const nextWidth = startWidth + (startX - moveEvent.clientX);
      setWidth(clamp(nextWidth, minWidth, maxWidth));
    };

    const handleUp = () => {
      document.removeEventListener("mousemove", handleMove);
      document.removeEventListener("mouseup", handleUp);
      document.body.style.cursor = "";
      document.body.style.userSelect = "";
    };

    document.body.style.cursor = "col-resize";
    document.body.style.userSelect = "none";
    document.addEventListener("mousemove", handleMove);
    document.addEventListener("mouseup", handleUp);
  };

  return {
    width,
    setWidth,
    startResize,
  };
}

function filterNodes(
  nodes: WorkspaceTreeNode[],
  query: string,
): WorkspaceTreeNode[] {
  if (!query) return nodes;

  const normalizedQuery = query.toLowerCase();
  const filtered = nodes
    .map((node) => {
      const matchedSelf =
        node.name.toLowerCase().includes(normalizedQuery) ||
        node.path.toLowerCase().includes(normalizedQuery);
      if (node.type === "file") {
        return matchedSelf ? node : null;
      }

      const filteredChildren = filterNodes(node.children || [], query);
      if (matchedSelf || filteredChildren.length > 0) {
        return {
          ...node,
          children: filteredChildren,
        };
      }

      return null;
    })
    .filter((node): node is WorkspaceTreeNode => Boolean(node));

  return filtered;
}

function collectFolderPaths(nodes: WorkspaceTreeNode[]) {
  const folderPaths: string[] = [];

  for (const node of nodes) {
    if (node.type === "folder") {
      folderPaths.push(node.path);
      folderPaths.push(...collectFolderPaths(node.children || []));
    }
  }

  return folderPaths;
}

function findTreeNodeByPath(
  nodes: WorkspaceTreeNode[],
  targetPath: string,
): WorkspaceTreeNode | null {
  for (const node of nodes) {
    if (node.path === targetPath) {
      return node;
    }
    if (node.children?.length) {
      const child = findTreeNodeByPath(node.children, targetPath);
      if (child) {
        return child;
      }
    }
  }

  return null;
}

function getNodeIcon(
  kind?: PreviewKind,
  isOpenFolder = false,
  isFile = false,
) {
  if (isOpenFolder) return FolderOpen;
  if (!kind) return isFile ? FileText : Folder;

  switch (kind) {
    case "code":
      return FileCode2;
    case "image":
      return ImageIcon;
    default:
      return FileText;
  }
}

function getChangeLabel(status: WorkspaceTreeNode["changeStatus"]) {
  switch (status) {
    case "added":
      return "workspace.preview.change_added";
    case "deleted":
      return "workspace.preview.change_deleted";
    default:
      return "workspace.preview.change_modified";
  }
}

function getChangeTone(status: WorkspaceTreeNode["changeStatus"]) {
  switch (status) {
    case "added":
      return "border-emerald-500/25 bg-emerald-500/10 text-emerald-300 hover:bg-emerald-500/15";
    case "deleted":
      return "border-rose-500/25 bg-rose-500/10 text-rose-300 hover:bg-rose-500/15";
    default:
      return "border-sky-500/25 bg-sky-500/10 text-sky-300 hover:bg-sky-500/15";
  }
}

function renderDiffLine(line: string, index: number) {
  let tone = "text-foreground";
  let gutterTone = "text-muted-foreground";

  if (line.startsWith("+")) {
    tone = "bg-emerald-500/10 text-emerald-200";
    gutterTone = "text-emerald-300";
  } else if (line.startsWith("-")) {
    tone = "bg-rose-500/10 text-rose-200";
    gutterTone = "text-rose-300";
  } else if (line.startsWith("@@")) {
    tone = "bg-sky-500/10 text-sky-200";
    gutterTone = "text-sky-300";
  }

  return (
    <div
      className={cn(
        "grid grid-cols-[auto_1fr] gap-4 px-4 py-0.5 font-mono text-[12px] leading-6",
        tone,
      )}
      key={`${line}-${index}`}
    >
      <span className={cn("select-none text-right text-[11px]", gutterTone)}>
        {index + 1}
      </span>
      <span className="overflow-x-auto whitespace-pre">{line || " "}</span>
    </div>
  );
}

function DiffPreview({ content }: { content?: string }) {
  const { t } = useI18n();
  const lines = (content || "").split("\n");

  return (
    <div className="flex h-full min-h-0 flex-col overflow-hidden rounded-[18px] border border-border bg-background/60">
      <div className="border-b border-border bg-card/80 px-4 py-2 text-[11px] uppercase tracking-[0.18em] text-muted-foreground">
        {t("workspace.preview.diff_label")}
      </div>
      <ScrollArea className="min-h-0 flex-1">
        <div className="py-3">{lines.map(renderDiffLine)}</div>
      </ScrollArea>
    </div>
  );
}

function PdfPreview({ content }: { content?: string }) {
  const sections = (content || "").split("\n\n").filter(Boolean);

  return (
    <div className="flex h-full min-h-[420px] items-center justify-center rounded-[22px] border border-border bg-[radial-gradient(circle_at_top,#2c3646,transparent_55%),linear-gradient(180deg,rgba(18,18,20,0.98),rgba(8,8,9,0.98))] p-5">
      <div className="w-full max-w-[640px] overflow-hidden rounded-[22px] bg-white text-zinc-900 shadow-[0_24px_80px_rgba(0,0,0,0.35)]">
        <div className="flex items-center justify-between border-b border-zinc-200 px-6 py-4">
          <div>
            <div className="text-[11px] font-semibold uppercase tracking-[0.24em] text-zinc-500">
              mock pdf viewer
            </div>
            <div className="mt-1 text-lg font-semibold">
              Implementation Review
            </div>
          </div>
          <div className="rounded-full bg-zinc-100 px-3 py-1 text-[11px] font-medium text-zinc-600">
            Page 1
          </div>
        </div>
        <div className="space-y-6 px-6 py-7">
          {sections.map((section) => {
            const [title, ...body] = section.split("\n");
            return (
              <section key={section}>
                <h4 className="text-[11px] font-semibold uppercase tracking-[0.2em] text-zinc-400">
                  {title}
                </h4>
                <div className="mt-2 space-y-2 text-sm leading-7 text-zinc-700">
                  {body.map((line) => (
                    <p key={line}>{line}</p>
                  ))}
                </div>
              </section>
            );
          })}
        </div>
      </div>
    </div>
  );
}

function ImagePreview({ src, title }: { src?: string; title: string }) {
  return (
    <div className="rounded-[22px] border border-border bg-[linear-gradient(180deg,rgba(22,22,24,0.98),rgba(10,10,11,0.98))] p-4">
      <div className="mb-3 flex items-center justify-between px-1 text-[11px] uppercase tracking-[0.18em] text-muted-foreground">
        <span>mock artifact</span>
        <span>{title}</span>
      </div>
      <div className="overflow-hidden rounded-[18px] border border-border bg-background/70">
        {src ? (
          <img alt={title} className="h-auto w-full object-cover" src={src} />
        ) : null}
      </div>
    </div>
  );
}

function HTMLPreviewFrame({ src, title }: { src?: string; title: string }) {
  return (
    <div className="flex h-full min-h-0 flex-col">
      <iframe
        className="min-h-0 w-full flex-1 border-0 bg-transparent"
        referrerPolicy="no-referrer"
        sandbox="allow-same-origin allow-scripts"
        src={src}
        title={title}
      />
    </div>
  );
}

function PreviewRenderer({
  document,
  workspaceId,
}: {
  document: WorkspacePreviewDocument | null;
  workspaceId?: string;
}) {
  const { t } = useI18n();

  if (!document) {
    return null;
  }

  if (document.isLoading) {
    return (
      <PreviewEmptyState
        description={t("workspace.preview.loading_file_desc")}
        title={t("workspace.preview.loading_file_title")}
      />
    );
  }

  if (document.kind === "markdown") {
    return (
      <ScrollArea className="h-full">
        <div className="mx-auto max-w-[860px] px-6 py-6">
          <MarkdownPreview
            content={document.content}
            filePath={document.path}
            workspaceId={workspaceId}
          />
        </div>
      </ScrollArea>
    );
  }

  if (document.kind === "code") {
    return <CodePreview content={document.content} language={document.language} />;
  }

  if (document.kind === "diff") {
    return (
      <div className="h-full px-4 py-4">
        <DiffPreview content={document.content} />
      </div>
    );
  }

  if (document.kind === "image") {
    return (
      <ScrollArea className="h-full">
        <div className="px-4 py-4">
          <ImagePreview src={document.src} title={document.title} />
        </div>
      </ScrollArea>
    );
  }

  if (document.kind === "html") {
    return <HTMLPreviewFrame src={document.src} title={document.title} />;
  }

  if (document.kind === "unsupported") {
    return (
      <PreviewEmptyState
        description={
          document.summary || t("workspace.preview.unsupported_desc")
        }
        title={`${t("workspace.preview.unsupported_title")}: ${document.title}`}
      />
    );
  }

  return (
    <div className="h-full px-4 py-4">
      {document.src ? (
        <iframe
          className="h-full w-full rounded-[18px] border border-border bg-white"
          src={document.src}
          title={document.title}
        />
      ) : (
        <PdfPreview content={document.content} />
      )}
    </div>
  );
}

function WorkspaceTree({
  nodes,
  expandedPaths,
  selectedPath,
  onOpenChange,
  onToggleFolder,
  onOpenFile,
  loadingDirectoryPaths,
  depth = 0,
}: {
  nodes: WorkspaceTreeNode[];
  expandedPaths: Set<string>;
  selectedPath: string | null;
  onOpenChange: (previewPath: string, selectedPath: string) => void;
  onToggleFolder: (path: string) => void;
  onOpenFile: (previewPath: string, selectedPath: string) => void;
  loadingDirectoryPaths: Record<string, boolean>;
  depth?: number;
}) {
  const { t } = useI18n();

  return (
    <div className="space-y-0.5">
      {nodes.map((node) => {
        const isFolder = node.type === "folder";
        const isExpanded = isFolder && expandedPaths.has(node.path);
        const isLoadingFolder = isFolder && Boolean(loadingDirectoryPaths[node.path]);
        const previewPath = node.previewPath || node.path;
        const isSelected = !isFolder && selectedPath === previewPath;
        const Icon = getNodeIcon(node.kind, isExpanded, !isFolder);
        const rowPadding = { paddingLeft: 12 + depth * 16 };

        if (isFolder) {
          return (
            <div key={node.path}>
              <button
                className={cn(
                  "flex w-full items-center gap-2 rounded-[12px] border border-transparent px-3 py-2 text-left text-sm transition",
                  "text-[rgb(var(--color-text-secondary))] hover:bg-accent hover:text-foreground",
                )}
                onClick={() => onToggleFolder(node.path)}
                style={rowPadding}
                type="button"
              >
                {isLoadingFolder ? (
                  <RefreshCw className="h-3.5 w-3.5 flex-shrink-0 animate-spin" />
                ) : isExpanded ? (
                  <ChevronDown className="h-3.5 w-3.5 flex-shrink-0" />
                ) : (
                  <ChevronRight className="h-3.5 w-3.5 flex-shrink-0" />
                )}
                <Icon className="h-4 w-4 flex-shrink-0 text-sky-300" />
                <span className="min-w-0 flex-1 truncate">{node.name}</span>
              </button>

              {isExpanded && node.children?.length ? (
                <WorkspaceTree
                  depth={depth + 1}
                  expandedPaths={expandedPaths}
                  loadingDirectoryPaths={loadingDirectoryPaths}
                  nodes={node.children}
                  onOpenChange={onOpenChange}
                  onOpenFile={onOpenFile}
                  onToggleFolder={onToggleFolder}
                  selectedPath={selectedPath}
                />
              ) : null}
            </div>
          );
        }

        const isDeleted = node.changeStatus === "deleted";
        const hasChange = Boolean(node.changeStatus && node.changePreviewPath);
        const iconTone = isDeleted
          ? "text-muted-foreground/50"
          : node.kind === "image"
            ? "text-emerald-300"
            : "text-muted-foreground";
        const nameTone = isDeleted
          ? "text-muted-foreground/60"
          : "text-inherit";

        return (
          <div
            className={cn(
              "flex items-center gap-2 rounded-[12px] border border-transparent px-3 py-2 text-sm transition",
              isSelected
                ? "bg-accent text-foreground"
                : "text-[rgb(var(--color-text-secondary))] hover:bg-accent hover:text-foreground",
            )}
            key={node.path}
            style={rowPadding}
          >
            <button
              className={cn(
                "flex min-w-0 flex-1 items-center gap-2 text-left",
                isDeleted ? "cursor-default" : "",
              )}
              disabled={isDeleted}
              onClick={() => onOpenFile(previewPath, node.path)}
              type="button"
            >
              <Icon className={cn("h-4 w-4 flex-shrink-0", iconTone)} />
              <span className={cn("min-w-0 flex-1 truncate", nameTone)}>
                {node.name}
              </span>
            </button>

            {hasChange ? (
              <button
                className={cn(
                  "inline-flex h-6 flex-shrink-0 items-center border px-2 text-[11px] font-medium transition",
                  getChangeTone(node.changeStatus),
                )}
                onClick={() => onOpenChange(node.changePreviewPath!, node.path)}
                title={t("workspace.preview.diff_label")}
                type="button"
              >
                {t(getChangeLabel(node.changeStatus))}
              </button>
            ) : null}
          </div>
        );
      })}
    </div>
  );
}

function PreviewEmptyState({
  title,
  description,
}: {
  title: string;
  description: string;
}) {
  return (
    <div className="flex h-full min-h-[280px] flex-col items-center justify-center gap-4 px-8 text-center">
      <div className="flex h-14 w-14 items-center justify-center rounded-full border border-border bg-background text-muted-foreground">
        <Eye className="h-6 w-6" />
      </div>
      <div>
        <h3 className="text-base font-semibold text-foreground">{title}</h3>
        <p className="mt-2 max-w-sm text-sm text-muted-foreground">
          {description}
        </p>
      </div>
    </div>
  );
}

function PreviewPanel({
  activeDocument,
  activePreviewPath,
  emptyContent,
  onActivateTab,
  onClosePanel,
  onCloseTab,
  onStartResize,
  persistent,
  workspaceId,
  tabs,
}: {
  activeDocument: WorkspacePreviewDocument | null;
  activePreviewPath: string | null;
  emptyContent?: ReactNode;
  onActivateTab: (path: string) => void;
  onClosePanel: () => void;
  onCloseTab: (path: string) => void;
  onStartResize?: (event: React.MouseEvent<HTMLDivElement>) => void;
  persistent?: boolean;
  workspaceId?: string;
  tabs: WorkspacePreviewDocument[];
}) {
  const { t } = useI18n();

  if (!persistent && tabs.length === 0 && !emptyContent) {
    return null;
  }

  return (
    <section className="group relative flex min-w-0 flex-1 flex-col overflow-hidden border border-border bg-card">
      {onStartResize ? (
        <div
          className="absolute -left-2 top-0 hidden h-full w-4 cursor-col-resize xl:block"
          onMouseDown={onStartResize}
          role="presentation"
        >
          <div className="mx-auto h-full w-px bg-border/0 transition group-hover:bg-border" />
        </div>
      ) : null}

      <div className="flex h-12 items-stretch border-b border-border bg-background/55">
        <div className="legacy-hidden-scrollbar flex min-w-0 flex-1 items-stretch overflow-x-auto px-2">
          {tabs.length ? (
            tabs.map((tab) => (
              <div
                className={cn(
                  "group/tab flex h-full min-w-[180px] max-w-[240px] flex-shrink-0 items-center gap-2 rounded-t-[14px] px-3 text-sm transition",
                  tab.path === activePreviewPath
                    ? "bg-card text-foreground"
                    : "text-muted-foreground hover:bg-background/75 hover:text-foreground",
                )}
                key={tab.path}
              >
                <button
                  className="min-w-0 flex-1 truncate text-left"
                  onClick={() => onActivateTab(tab.path)}
                  type="button"
                >
                  {tab.title}
                </button>
                <button
                  className="rounded-sm p-1 text-muted-foreground transition hover:bg-background hover:text-foreground"
                  onClick={() => onCloseTab(tab.path)}
                  type="button"
                >
                  <X className="h-3.5 w-3.5" />
                </button>
              </div>
            ))
          ) : (
            <div className="flex items-center px-3 text-xs uppercase tracking-[0.18em] text-muted-foreground">
              {t("workspace.preview.tabs_empty")}
            </div>
          )}
        </div>
        <div className="flex items-center px-2">
          <Button
            className="rounded-[10px]"
            onClick={onClosePanel}
            size="icon"
            title={t("workspace.preview.close_preview")}
            type="button"
            variant="ghost"
          >
            <PanelRightClose className="h-4 w-4" />
          </Button>
        </div>
      </div>

      {activeDocument ? (
        <div className="min-h-0 flex-1 bg-[linear-gradient(180deg,rgba(15,15,17,0.96),rgba(10,10,11,0.98))]">
          <PreviewRenderer
            document={activeDocument}
            workspaceId={workspaceId}
          />
        </div>
      ) : (
        emptyContent || (
          <PreviewEmptyState
            description={t("workspace.preview.empty_desc")}
            title={t("workspace.preview.empty_title")}
          />
        )
      )}
    </section>
  );
}

function WorkspacePanelContent({
  expandedPaths,
  filteredNodes,
  isLoadingTree,
  loadingDirectoryPaths,
  onClose,
  onOpenChange,
  onOpenFile,
  onRefresh,
  onResetSearch,
  onSearchChange,
  onToggleFolder,
  searchQuery,
  selectedPath,
  showClose,
  workspaceError,
  workspaceNotice,
  workspaceName,
}: {
  expandedPaths: Set<string>;
  filteredNodes: WorkspaceTreeNode[];
  isLoadingTree: boolean;
  loadingDirectoryPaths: Record<string, boolean>;
  onClose?: () => void;
  onOpenChange: (previewPath: string, path: string) => void;
  onOpenFile: (previewPath: string, selectedPath: string) => void;
  onRefresh?: () => void;
  onResetSearch: () => void;
  onSearchChange: (value: string) => void;
  onToggleFolder: (path: string) => void;
  searchQuery: string;
  selectedPath: string | null;
  showClose?: boolean;
  workspaceError?: string | null;
  workspaceNotice?: ReactNode;
  workspaceName?: string;
}) {
  const { t } = useI18n();

  return (
    <section className="flex h-full min-h-0 min-w-0 w-full flex-1 flex-col overflow-hidden border border-border bg-card">
      <div className="border-b border-border px-3 py-3">
        <div className="flex items-center gap-2">
          <div className="relative min-w-0 flex-1">
            <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              className="rounded-[12px] border-border bg-background pl-9 pr-12"
              onChange={(event) => onSearchChange(event.target.value)}
              placeholder={t("workspace.preview.search_placeholder")}
              value={searchQuery}
            />
            {searchQuery ? (
              <button
                className="absolute right-2 top-1/2 flex h-7 w-7 -translate-y-1/2 items-center justify-center rounded-[9px] text-muted-foreground transition hover:bg-accent hover:text-foreground"
                onClick={onResetSearch}
                type="button"
              >
                <X className="h-3.5 w-3.5" />
              </button>
            ) : null}
          </div>
          {onRefresh ? (
            <Button
              className="rounded-[10px]"
              disabled={isLoadingTree}
              onClick={onRefresh}
              size="icon"
              title={t("workspace.preview.refresh_workspace")}
              type="button"
              variant="ghost"
            >
              <RefreshCw
                className={cn("h-4 w-4", isLoadingTree ? "animate-spin" : "")}
              />
            </Button>
          ) : null}
          {showClose && onClose ? (
            <Button
              className="h-8 w-8 rounded-md border-border bg-card text-[rgb(var(--color-text-secondary))] hover:bg-accent hover:text-foreground"
              onClick={onClose}
              size="icon"
              title={t("workspace.preview.close_workspace")}
              type="button"
              variant="outline"
            >
              <PanelRightClose className="h-4 w-4" />
            </Button>
          ) : null}
        </div>
      </div>

      <div className="min-h-0 flex-1 bg-[linear-gradient(180deg,rgba(15,15,17,0.96),rgba(10,10,11,0.98))]">
        <ScrollArea className="h-full px-3 py-3">
          {workspaceName ? (
            isLoadingTree ? (
              <PreviewEmptyState
                description={t("workspace.preview.loading_tree_desc")}
                title={t("workspace.preview.loading_tree_title")}
              />
            ) : workspaceNotice ? (
              <div className="space-y-3">
                {workspaceNotice}
                {filteredNodes.length ? (
                  <WorkspaceTree
                    expandedPaths={expandedPaths}
                    loadingDirectoryPaths={loadingDirectoryPaths}
                    nodes={filteredNodes}
                    onOpenChange={onOpenChange}
                    onOpenFile={onOpenFile}
                    onToggleFolder={onToggleFolder}
                    selectedPath={selectedPath}
                  />
                ) : null}
              </div>
            ) : workspaceError ? (
              <PreviewEmptyState
                description={workspaceError}
                title="Workspace unavailable"
              />
            ) : filteredNodes.length ? (
              <WorkspaceTree
                expandedPaths={expandedPaths}
                loadingDirectoryPaths={loadingDirectoryPaths}
                nodes={filteredNodes}
                onOpenChange={onOpenChange}
                onOpenFile={onOpenFile}
                onToggleFolder={onToggleFolder}
                selectedPath={selectedPath}
              />
            ) : (
              <PreviewEmptyState
                description={t("workspace.preview.no_matches_desc")}
                title={t("workspace.preview.no_matches_title")}
              />
            )
          ) : (
            <PreviewEmptyState
              description={t("workspace.preview.no_workspace_desc")}
              title={t("workspace.preview.no_workspace")}
            />
          )}
        </ScrollArea>
      </div>
    </section>
  );
}

export function WorkspacePreviewPane({
  onRefreshWorkspaceStatus,
  refreshToken = 0,
  workspace,
}: {
  onRefreshWorkspaceStatus?: () => void;
  refreshToken?: number;
  workspace: Workspace | null;
}) {
  const { t } = useI18n();
  const isCompact = useCompactLayout();
  const previewPanel = useResizableWidth(430, 340, 720);
  const workspacePanel = useResizableWidth(320, 280, 420);
  const [searchQuery, setSearchQuery] = useState("");
  const [expandedPaths, setExpandedPaths] = useState<string[]>([]);
  const [selectedPath, setSelectedPath] = useState<string | null>(null);
  const [previewTabs, setPreviewTabs] = useState<WorkspacePreviewDocument[]>(
    [],
  );
  const [activePreviewPath, setActivePreviewPath] = useState<string | null>(
    null,
  );
  const [workspaceCollapsed, setWorkspaceCollapsed] = useState(true);
  const [mobileOpen, setMobileOpen] = useState(false);
  const {
    isLoadingTree,
    loadDirectory,
    loadingDirectoryPaths,
    loadDocument,
    loadError,
    model,
    reloadTree,
  } =
    useWorkspacePreviewModel(workspace, refreshToken);
  const isWorkspaceBlocked = isWorkspaceInteractionBlocked(workspace);

  useEffect(() => {
    setSearchQuery("");
    setSelectedPath(null);
    setPreviewTabs([]);
    setActivePreviewPath(null);
    setExpandedPaths([]);
  }, [model?.workspaceId]);

  useEffect(() => {
    if (!isCompact) {
      setMobileOpen(false);
    }
  }, [isCompact]);

  const filteredNodes = useMemo(
    () => filterNodes(model?.nodes || [], searchQuery),
    [model?.nodes, searchQuery],
  );
  const visibleExpandedPaths = useMemo(
    () =>
      new Set(searchQuery ? collectFolderPaths(filteredNodes) : expandedPaths),
    [expandedPaths, filteredNodes, searchQuery],
  );
  const activeDocument =
    previewTabs.find((tab) => tab.path === activePreviewPath) || null;
  const retryWorkspaceAccess = () => {
    void reloadTree();
    onRefreshWorkspaceStatus?.();
  };
  const workspaceNotice = isWorkspaceBlocked ? (
    <SandboxWorkspaceAlert
      compact={false}
      onRetry={retryWorkspaceAccess}
      workspace={workspace}
    />
  ) : null;
  const previewEmptyContent = isWorkspaceBlocked ? (
    <div className="p-4">
      <SandboxWorkspaceAlert
        compact={false}
        onRetry={retryWorkspaceAccess}
        workspace={workspace}
      />
    </div>
  ) : loadError ? (
    <PreviewEmptyState
      description={loadError}
      title="Workspace unavailable"
    />
  ) : undefined;

  const openDocument = async (
    previewPath: string,
    nextSelectedPath: string,
  ) => {
    if (!model) return;

    const existingDocument = previewTabs.find(
      (item) => item.path === previewPath,
    );
    if (existingDocument) {
      setSelectedPath(nextSelectedPath);
      setActivePreviewPath(existingDocument.path);
      if (isCompact) {
        setMobileOpen(true);
      }
      return;
    }

    const node = findTreeNodeByPath(model.nodes, nextSelectedPath);
    const isDiffDocument = previewPath.startsWith("__changes__/");
    const placeholderDocument: WorkspacePreviewDocument = {
      path: previewPath,
      title: isDiffDocument
        ? `${nextSelectedPath.split("/").pop() || nextSelectedPath}.diff`
        : node?.name || previewPath.split("/").pop() || previewPath,
      kind: isDiffDocument ? "diff" : node?.kind || "unsupported",
      summary: t("workspace.preview.loading_file_title"),
      source: model.source,
      isLoading: true,
    };

    setPreviewTabs((current) => {
      return [...current, placeholderDocument];
    });
    setSelectedPath(nextSelectedPath);
    setActivePreviewPath(previewPath);
    if (isCompact) {
      setMobileOpen(true);
    }

    const loadedDocument = await loadDocument(
      previewPath,
      isDiffDocument ? "diff" : node?.kind,
    );
    if (!loadedDocument) {
      setPreviewTabs((current) =>
        current.filter((item) => item.path !== previewPath),
      );
      if (activePreviewPath === previewPath) {
        setActivePreviewPath((current) =>
          current === previewPath ? null : current,
        );
      }
      return;
    }

    setPreviewTabs((current) =>
      current.map((item) =>
        item.path === previewPath ? loadedDocument : item,
      ),
    );
  };

  const closePreviewTab = (path: string) => {
    setPreviewTabs((current) => {
      const nextTabs = current.filter((tab) => tab.path !== path);

      if (activePreviewPath === path) {
        setActivePreviewPath(nextTabs[nextTabs.length - 1]?.path || null);
      }

      return nextTabs;
    });
  };

  const closePreviewPanel = () => {
    setPreviewTabs([]);
    setActivePreviewPath(null);
  };

  const toggleFolder = (path: string) => {
    if (!model) {
      return;
    }

    const node = findTreeNodeByPath(model.nodes, path);
    const shouldExpand = !expandedPaths.includes(path);
    setExpandedPaths((current) => {
      if (current.includes(path)) {
        return current.filter((item) => item !== path);
      }

      return [...current, path];
    });
    if (shouldExpand && node?.type === "folder" && !node.loaded) {
      void loadDirectory(path);
    }
  };

  const workspaceContent = (
    <WorkspacePanelContent
      expandedPaths={visibleExpandedPaths}
      filteredNodes={filteredNodes}
      isLoadingTree={isLoadingTree}
      loadingDirectoryPaths={loadingDirectoryPaths}
      onClose={() => setWorkspaceCollapsed(true)}
      onOpenChange={openDocument}
      onOpenFile={openDocument}
      onRefresh={workspace ? retryWorkspaceAccess : undefined}
      onResetSearch={() => setSearchQuery("")}
      onSearchChange={setSearchQuery}
      onToggleFolder={toggleFolder}
      searchQuery={searchQuery}
      selectedPath={selectedPath}
      showClose={!isCompact}
      workspaceError={loadError}
      workspaceNotice={workspaceNotice}
      workspaceName={model?.workspaceName}
    />
  );

  if (isCompact) {
    return (
      <>
        <div className="fixed bottom-4 right-4 z-40 xl:hidden">
          <Button
            className="h-11 w-11 rounded-full border-border bg-card text-foreground shadow-floating"
            onClick={() => setMobileOpen(true)}
            size="icon"
            title={t("workspace.preview.open_workspace")}
            type="button"
            variant="outline"
          >
            <PanelRightOpen className="h-4 w-4" />
          </Button>
        </div>

        {mobileOpen ? (
          <>
            <button
              aria-label={t("workspace.preview.close_workspace")}
              className="fixed inset-0 z-40 bg-background/72 backdrop-blur-sm"
              onClick={() => setMobileOpen(false)}
              type="button"
            />
            <div className="fixed inset-x-3 bottom-3 top-20 z-50 overflow-hidden rounded-[26px] border border-border bg-background/95 shadow-floating">
              <div className="flex items-center justify-between border-b border-border px-4 py-3">
                <div>
                  <div className="text-sm font-semibold text-foreground">
                    {model?.workspaceName ||
                      t("workspace.preview.no_workspace")}
                  </div>
                </div>
                <Button
                  className="rounded-[10px]"
                  onClick={() => setMobileOpen(false)}
                  size="icon"
                  type="button"
                  variant="ghost"
                >
                  <X className="h-4 w-4" />
                </Button>
              </div>

              <div className="grid h-[calc(100%-61px)] min-h-0 grid-rows-[minmax(0,0.9fr)_minmax(0,1.1fr)] gap-3 p-3">
                <div className="min-h-0">{workspaceContent}</div>
                <div className="min-h-0">
                  <PreviewPanel
                    activeDocument={activeDocument}
                    activePreviewPath={activePreviewPath}
                    emptyContent={previewEmptyContent}
                    onActivateTab={setActivePreviewPath}
                    onClosePanel={closePreviewPanel}
                    onCloseTab={closePreviewTab}
                    persistent
                    tabs={previewTabs}
                  />
                </div>
              </div>
            </div>
          </>
        ) : null}
      </>
    );
  }

  return (
    <>
      {previewTabs.length || isWorkspaceBlocked || Boolean(loadError) ? (
        <div
          className="relative flex h-full min-w-[340px] flex-shrink-0"
          style={{ width: previewPanel.width }}
        >
          <PreviewPanel
            activeDocument={activeDocument}
            activePreviewPath={activePreviewPath}
            emptyContent={previewEmptyContent}
            onActivateTab={setActivePreviewPath}
            onClosePanel={closePreviewPanel}
            onCloseTab={closePreviewTab}
            onStartResize={previewPanel.startResize}
            persistent={isWorkspaceBlocked || Boolean(loadError)}
            tabs={previewTabs}
            workspaceId={model?.workspaceId}
          />
        </div>
      ) : null}

      {workspaceCollapsed ? (
        <div className="flex h-full flex-shrink-0 items-start px-4 pt-4">
          <Button
            className="h-8 w-8 rounded-md border-border bg-card text-[rgb(var(--color-text-secondary))] hover:bg-accent hover:text-foreground"
            onClick={() => setWorkspaceCollapsed(false)}
            size="icon"
            title={t("workspace.preview.open_workspace")}
            type="button"
            variant="outline"
          >
            <PanelRightOpen className="h-4 w-4" />
          </Button>
        </div>
      ) : (
        <div
          className="group relative flex h-full min-w-[280px] flex-shrink-0"
          style={{ width: workspacePanel.width }}
        >
          <div
            className="absolute -left-2 top-0 hidden h-full w-4 cursor-col-resize xl:block"
            onMouseDown={workspacePanel.startResize}
            role="presentation"
          >
            <div className="mx-auto h-full w-px bg-border/0 transition group-hover:bg-border" />
          </div>
          {workspaceContent}
        </div>
      )}
    </>
  );
}
