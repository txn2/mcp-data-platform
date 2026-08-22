import type { ReactNode } from "react";
import type { Asset, AssetVersion, SharePermission } from "@/api/portal/types";

export interface MutationLike<TVariables> {
  mutate: (vars: TVariables, options?: { onSuccess?: () => void; onError?: () => void }) => void;
  isPending: boolean;
}

export type ViewMode = "preview" | "source";

export interface AssetViewerProps {
  asset: Asset | undefined;
  content: string | ArrayBuffer | undefined;
  isLoading: boolean;
  contentUrl: string;
  onBack: () => void;
  onNavigate: (path: string) => void;
  updateMutation: MutationLike<{
    id: string;
    name: string;
    description: string;
    tags: string[];
    // Optional: an update that did not move retention leaves the field out,
    // which is how an editor-share recipient saves a rename without sending the
    // one field the API reserves to the owner.
    max_versions?: number | null;
  }>;
  deleteMutation: MutationLike<string>;
  contentUpdateMutation?: MutationLike<{ id: string; content: string; changeSummary?: string }>;
  copyMutation?: MutationLike<string>;
  isOwner?: boolean;
  sharePermission?: SharePermission;
  toolbarExtra?: ReactNode;
  detailRows?: { label: string; value: ReactNode }[];
  /**
   * Where the session that produced this asset opens, if the reader can open
   * it at all. An operator reads it on the admin sessions surface, an owner on
   * their own; a reader who is neither is given no link, because the session
   * would answer them not-found (#1319).
   */
  sessionPath?: (sessionId: string) => string;
  versions?: AssetVersion[];
  versionsLoading?: boolean;
  revertMutation?: MutationLike<{ assetId: string; version: number }>;
  selectedVersion?: number | null;
  onSelectVersion?: (v: number | null) => void;
  versionContent?: string;
  versionContentLoading?: boolean;
}
