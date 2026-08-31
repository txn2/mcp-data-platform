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
  /**
   * Where a managed resource this asset references opens for this reader. The
   * portal and the admin console hold the same resource at different
   * addresses, so the surface supplies it; absent, a referenced file is named
   * without being linked (#1475).
   */
  resourcePath?: (resourceId: string) => string;
  /** Where another asset this one references opens for this reader, per
   * surface. Absent, a referenced asset is named without being linked. */
  assetPath?: (assetId: string) => string;
  /** Where a managed script that produced this asset opens for this reader
   * (#1569). The portal and the admin console hold the same script at
   * different addresses; absent, a producing script is named without being
   * linked, which is also what a deleted one gets. */
  scriptPath?: (scriptId: string) => string;
  /**
   * Which route this reader reads an asset's stored thumbnail through. The
   * portal's view grant has no admin arm, so the console reads a tile it is
   * showing an operator through the admin route (#1292); absent, the portal
   * route is used.
   */
  assetApiBase?: string;
  versions?: AssetVersion[];
  versionsLoading?: boolean;
  revertMutation?: MutationLike<{ assetId: string; version: number }>;
  selectedVersion?: number | null;
  onSelectVersion?: (v: number | null) => void;
  versionContent?: string;
  versionContentLoading?: boolean;
}
