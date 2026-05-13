export type PreviewKind =
  | "code"
  | "markdown"
  | "image"
  | "pdf"
  | "html"
  | "diff"
  | "unsupported";

export interface WorkspacePreviewDocument {
  path: string;
  title: string;
  kind: PreviewKind;
  content?: string;
  src?: string;
  language?: string;
  summary?: string;
  updatedAtLabel?: string;
  mime?: string;
  source: "live" | "mock";
  isLoading?: boolean;
}

export interface WorkspaceTreeNode {
  path: string;
  name: string;
  type: "folder" | "file";
  kind?: PreviewKind;
  previewPath?: string;
  loaded?: boolean;
  changeStatus?: WorkspaceChange["status"];
  changePreviewPath?: string;
  isDeletedSynthetic?: boolean;
  isSynthetic?: boolean;
  children?: WorkspaceTreeNode[];
}

export interface WorkspaceChange {
  id: string;
  path: string;
  status: "added" | "modified" | "deleted";
  summary?: string;
  insertions?: number;
  deletions?: number;
  previewPath: string;
}

export interface WorkspacePreviewModel {
  workspaceId: string;
  workspaceName: string;
  workspacePath: string;
  nodes: WorkspaceTreeNode[];
  documents: Record<string, WorkspacePreviewDocument>;
  changes: WorkspaceChange[];
  source: "live" | "mock";
}
