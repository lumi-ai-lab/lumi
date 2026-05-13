"use client";

import { useCallback, useEffect, useRef, useState } from "react";

import {
  buildWorkspaceHTMLPreviewUrl,
  fetchWorkspaceChanges,
  fetchWorkspaceDiff,
  buildWorkspaceFileBufferUrl,
  fetchWorkspaceFileMeta,
  fetchWorkspaceTextFile,
  fetchWorkspaceTree,
} from "@/lib/api";
import type {
  Workspace,
  WorkspaceFileChange,
  WorkspaceFileMeta,
  WorkspacePreviewKind,
  WorkspaceTreeEntry,
} from "@/lib/types";
import { formatFileSize, formatRelativeTime } from "@/lib/utils";
import { inferPreviewLanguage } from "@/features/workspace-preview/language";
import type {
  PreviewKind,
  WorkspaceChange,
  WorkspacePreviewDocument,
  WorkspacePreviewModel,
  WorkspaceTreeNode,
} from "@/features/workspace-preview/types";

const changePreviewPrefix = "__changes__/";

function mapPreviewKind(kind?: WorkspacePreviewKind): PreviewKind {
  switch (kind) {
    case "markdown":
      return "markdown";
    case "image":
      return "image";
    case "pdf":
      return "pdf";
    case "html":
      return "html";
    case "unsupported":
      return "unsupported";
    default:
      return "code";
  }
}

function toWorkspaceTreeNode(entry: WorkspaceTreeEntry): WorkspaceTreeNode {
  return {
    path: entry.path,
    name: entry.name,
    type: entry.isDir ? "folder" : "file",
    kind: entry.isDir ? undefined : mapPreviewKind(entry.previewKind),
    previewPath: entry.path,
    loaded: entry.isDir ? Boolean(entry.loaded) : true,
    children: entry.children?.map(toWorkspaceTreeNode),
  };
}

function buildChangePreviewPath(path: string) {
  return `${changePreviewPrefix}${path}`;
}

function isChangePreviewPath(path: string) {
  return path.startsWith(changePreviewPrefix);
}

function extractChangePath(path: string) {
  return path.startsWith(changePreviewPrefix)
    ? path.slice(changePreviewPrefix.length)
    : path;
}

function buildChangeSummary(
  change: Pick<WorkspaceFileChange, "insertions" | "deletions">,
) {
  const parts: string[] = [];
  if (change.insertions) {
    parts.push(`+${change.insertions}`);
  }
  if (change.deletions) {
    parts.push(`-${change.deletions}`);
  }

  return parts.join(" ");
}

function toWorkspaceChange(change: WorkspaceFileChange) {
  return {
    id: `${change.status}:${change.path}`,
    path: change.path,
    status: change.status,
    summary: buildChangeSummary(change),
    insertions: change.insertions,
    deletions: change.deletions,
    previewPath: buildChangePreviewPath(change.path),
  };
}

function compareTreeNodes(a: WorkspaceTreeNode, b: WorkspaceTreeNode) {
  if (a.type !== b.type) {
    return a.type === "folder" ? -1 : 1;
  }

  return a.name.localeCompare(b.name, undefined, {
    numeric: true,
    sensitivity: "base",
  });
}

function insertNodeSorted(nodes: WorkspaceTreeNode[], node: WorkspaceTreeNode) {
  const insertAt = nodes.findIndex(
    (current) => compareTreeNodes(node, current) < 0,
  );

  if (insertAt === -1) {
    nodes.push(node);
    return;
  }

  nodes.splice(insertAt, 0, node);
}

function decorateNodesWithChanges(
  nodes: WorkspaceTreeNode[],
  changeMap: Map<string, WorkspaceChange>,
  existingPaths: Set<string>,
): WorkspaceTreeNode[] {
  return nodes.map((node) => {
    existingPaths.add(node.path);

    const nextNode: WorkspaceTreeNode = {
      ...node,
      children: node.children
        ? decorateNodesWithChanges(node.children, changeMap, existingPaths)
        : undefined,
    };

    if (nextNode.type === "file") {
      const change = changeMap.get(nextNode.path);
      if (change) {
        nextNode.changeStatus = change.status;
        nextNode.changePreviewPath = change.previewPath;
      }
    }

    return nextNode;
  });
}

function mergeTreeWithChanges(
  nodes: WorkspaceTreeNode[],
  changes: WorkspaceChange[],
) {
  const changeMap = new Map(changes.map((change) => [change.path, change]));
  const existingPaths = new Set<string>();
  const mergedNodes = decorateNodesWithChanges(nodes, changeMap, existingPaths);

  for (const change of changes) {
    if (change.status !== "deleted" || existingPaths.has(change.path)) {
      continue;
    }

    const segments = change.path.split("/").filter(Boolean);
    if (!segments.length) {
      continue;
    }

    let currentNodes = mergedNodes;
    let currentPath = "";

    for (const segment of segments.slice(0, -1)) {
      currentPath = currentPath ? `${currentPath}/${segment}` : segment;

      let folderNode = currentNodes.find(
        (node) => node.type === "folder" && node.path === currentPath,
      );

      if (!folderNode) {
        folderNode = {
          path: currentPath,
          name: segment,
          type: "folder",
          isSynthetic: true,
          children: [],
        };
        insertNodeSorted(currentNodes, folderNode);
      }

      if (!folderNode.children) {
        folderNode.children = [];
      }
      currentNodes = folderNode.children;
    }

    const fileName = segments[segments.length - 1];
    if (currentNodes.some((node) => node.type === "file" && node.path === change.path)) {
      continue;
    }

    insertNodeSorted(currentNodes, {
      path: change.path,
      name: fileName,
      type: "file",
      changeStatus: change.status,
      changePreviewPath: change.previewPath,
      isDeletedSynthetic: true,
    });
  }

  return mergedNodes;
}

function updateTreeNodeChildren(
  nodes: WorkspaceTreeNode[],
  targetPath: string,
  children: WorkspaceTreeNode[],
): WorkspaceTreeNode[] {
  return nodes.map((node) => {
    if (node.path === targetPath && node.type === "folder") {
      return {
        ...node,
        children,
        loaded: true,
      };
    }

    if (!node.children?.length) {
      return node;
    }

    return {
      ...node,
      children: updateTreeNodeChildren(node.children, targetPath, children),
    };
  });
}

function changesForLoadedDirectory(
  changes: WorkspaceChange[],
  directoryPath: string,
) {
  const prefix = `${directoryPath}/`;
  return changes.filter((change) => change.path.startsWith(prefix));
}

function buildSummary(meta: WorkspaceFileMeta, truncated?: boolean) {
  const parts = [formatFileSize(meta.size)];
  if (meta.mime) {
    parts.push(meta.mime);
  }
  if (truncated) {
    parts.push("preview truncated");
  }

  return parts.join(" • ");
}

function buildTextDocument(
  meta: WorkspaceFileMeta,
  content: string,
  truncated?: boolean,
): WorkspacePreviewDocument {
  return {
    path: meta.path,
    title: meta.name,
    kind: mapPreviewKind(meta.previewKind),
    content,
    language: inferPreviewLanguage(meta.path),
    summary: buildSummary(meta, truncated),
    updatedAtLabel: formatRelativeTime(meta.modifiedAt),
    mime: meta.mime,
    source: "live",
  };
}

function buildBinaryDocument(
  workspaceId: string,
  meta: WorkspaceFileMeta,
): WorkspacePreviewDocument {
  return {
    path: meta.path,
    title: meta.name,
    kind: mapPreviewKind(meta.previewKind),
    src: buildWorkspaceFileBufferUrl(workspaceId, meta.path),
    summary: buildSummary(meta),
    updatedAtLabel: formatRelativeTime(meta.modifiedAt),
    mime: meta.mime,
    source: "live",
  };
}

function buildHTMLDocument(
  workspaceId: string,
  meta: WorkspaceFileMeta,
): WorkspacePreviewDocument {
  return {
    path: meta.path,
    title: meta.name,
    kind: "html",
    src: buildWorkspaceHTMLPreviewUrl(workspaceId, meta.path),
    summary: buildSummary(meta),
    updatedAtLabel: formatRelativeTime(meta.modifiedAt),
    mime: meta.mime,
    source: "live",
  };
}

function buildUnsupportedDocument(
  path: string,
  message: string,
): WorkspacePreviewDocument {
  return {
    path,
    title: path.split("/").pop() || path,
    kind: "unsupported",
    summary: message,
    source: "live",
  };
}

function buildDiffDocument(
  previewPath: string,
  path: string,
  content: string,
): WorkspacePreviewDocument {
  const name = path.split("/").pop() || path;

  return {
    path: previewPath,
    title: `${name}.diff`,
    kind: "diff",
    content,
    summary: "",
    source: "live",
  };
}

function createEmptyLiveModel(workspace: Workspace): WorkspacePreviewModel {
  return {
    workspaceId: workspace.id,
    workspaceName: workspace.name,
    workspacePath: workspace.path,
    nodes: [],
    documents: {},
    changes: [],
    source: "live",
  };
}

export function useWorkspacePreviewModel(
  workspace: Workspace | null,
  refreshToken = 0,
) {
  const [model, setModel] = useState<WorkspacePreviewModel | null>(
    workspace ? createEmptyLiveModel(workspace) : null,
  );
  const [documents, setDocuments] = useState<
    Record<string, WorkspacePreviewDocument>
  >({});
  const [isLoadingTree, setIsLoadingTree] = useState(false);
  const [loadingDirectoryPaths, setLoadingDirectoryPaths] = useState<
    Record<string, boolean>
  >({});
  const [loadError, setLoadError] = useState<string | null>(null);
  const treeRequestIdRef = useRef(0);
  const lastRefreshTokenRef = useRef(refreshToken);

  const loadTree = useCallback(
    async ({
      resetState = false,
    }: {
      resetState?: boolean;
    } = {}) => {
      if (!workspace) {
        setModel(null);
        setDocuments({});
        setIsLoadingTree(false);
        setLoadError(null);
        return;
      }

      const requestId = ++treeRequestIdRef.current;

      if (resetState) {
        setModel(createEmptyLiveModel(workspace));
        setDocuments({});
      }
      setIsLoadingTree(true);
      setLoadError(null);

      try {
        const [treeResult, changesResult] = await Promise.allSettled([
          fetchWorkspaceTree(workspace.id),
          fetchWorkspaceChanges(workspace.id),
        ]);
        if (treeRequestIdRef.current !== requestId) return;
        if (treeResult.status === "rejected") {
          throw treeResult.reason;
        }

        const nextChanges =
          changesResult.status === "fulfilled"
            ? changesResult.value.map(toWorkspaceChange)
            : [];
        const nextNodes = mergeTreeWithChanges(
          treeResult.value.map(toWorkspaceTreeNode),
          nextChanges,
        );
        let shouldClearDocuments = resetState;

        setModel((current) => {
          if (current?.source === "mock") {
            shouldClearDocuments = true;
          }

          const baseModel =
            current &&
            current.workspaceId === workspace.id &&
            current.source === "live" &&
            !resetState
              ? current
              : createEmptyLiveModel(workspace);

          return {
            ...baseModel,
            workspaceId: workspace.id,
            workspaceName: workspace.name,
            workspacePath: workspace.path,
            nodes: nextNodes,
            documents: {},
            changes: nextChanges,
            source: "live",
          };
        });

        if (shouldClearDocuments) {
          setDocuments({});
        }
      } catch (error) {
        if (treeRequestIdRef.current !== requestId) return;
        const message =
          error instanceof Error ? error.message : "Unable to load workspace";
        setLoadError(message);
      } finally {
        if (treeRequestIdRef.current === requestId) {
          setIsLoadingTree(false);
        }
      }
    },
    [workspace],
  );

  const loadDirectory = useCallback(
    async (path: string) => {
      if (!workspace || !model) {
        return;
      }

      setLoadingDirectoryPaths((current) => ({ ...current, [path]: true }));
      try {
        const children = (await fetchWorkspaceTree(workspace.id, path)).map(
          toWorkspaceTreeNode,
        );
        setModel((current) => {
          if (!current || current.workspaceId !== workspace.id) {
            return current;
          }
          const nextChildren = mergeTreeWithChanges(
            children,
            changesForLoadedDirectory(current.changes, path),
          );

          return {
            ...current,
            nodes: updateTreeNodeChildren(current.nodes, path, nextChildren),
          };
        });
      } finally {
        setLoadingDirectoryPaths((current) => {
          const next = { ...current };
          delete next[path];
          return next;
        });
      }
    },
    [model, workspace],
  );

  useEffect(() => {
    if (!workspace) {
      setModel(null);
      setDocuments({});
      setIsLoadingTree(false);
      setLoadError(null);
      return;
    }

    void loadTree({ resetState: true });
  }, [loadTree, workspace?.id, workspace?.name, workspace?.path]);

  useEffect(() => {
    if (!workspace) {
      lastRefreshTokenRef.current = refreshToken;
      return;
    }
    if (refreshToken === lastRefreshTokenRef.current) {
      return;
    }

    lastRefreshTokenRef.current = refreshToken;
    void loadTree();
  }, [loadTree, refreshToken, workspace?.id]);

  const loadDocument = useCallback(
    async (path: string, kindHint?: PreviewKind) => {
      if (documents[path]) {
        return documents[path];
      }
      if (!workspace || !model) {
        return null;
      }

      try {
        if (isChangePreviewPath(path) || kindHint === "diff") {
          const changePath = extractChangePath(path);
          const diff = await fetchWorkspaceDiff(workspace.id, changePath);
          const document = buildDiffDocument(path, changePath, diff.content);
          setDocuments((current) => ({ ...current, [path]: document }));
          return document;
        }

        if (
          kindHint === "image" ||
          kindHint === "pdf" ||
          kindHint === "html" ||
          kindHint === "unsupported"
        ) {
          const meta = await fetchWorkspaceFileMeta(workspace.id, path);
          const document =
            meta.previewKind === "unsupported"
              ? buildUnsupportedDocument(path, buildSummary(meta))
              : meta.previewKind === "html"
                ? buildHTMLDocument(workspace.id, meta)
                : buildBinaryDocument(workspace.id, meta);
          setDocuments((current) => ({ ...current, [path]: document }));
          return document;
        }

        const textFile = await fetchWorkspaceTextFile(workspace.id, path);
        const document = buildTextDocument(
          textFile.meta,
          textFile.content,
          textFile.truncated,
        );
        setDocuments((current) => ({ ...current, [path]: document }));
        return document;
      } catch (error) {
        const message =
          error instanceof Error ? error.message : "Unable to load preview";
        const fallbackDocument = buildUnsupportedDocument(path, message);
        setDocuments((current) => ({ ...current, [path]: fallbackDocument }));
        return fallbackDocument;
      }
    },
    [documents, model, workspace],
  );

  return {
    documents,
    isLoadingTree,
    loadingDirectoryPaths,
    loadError,
    loadDirectory,
    loadDocument,
    model,
    reloadTree: () => loadTree(),
  };
}
