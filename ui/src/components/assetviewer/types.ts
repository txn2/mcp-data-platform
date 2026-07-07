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
  updateMutation: MutationLike<{ id: string; name: string; description: string; tags: string[] }>;
  deleteMutation: MutationLike<string>;
  contentUpdateMutation?: MutationLike<{ id: string; content: string; changeSummary?: string }>;
  copyMutation?: MutationLike<string>;
  isOwner?: boolean;
  sharePermission?: SharePermission;
  toolbarExtra?: ReactNode;
  detailRows?: { label: string; value: ReactNode }[];
  versions?: AssetVersion[];
  versionsLoading?: boolean;
  revertMutation?: MutationLike<{ assetId: string; version: number }>;
  selectedVersion?: number | null;
  onSelectVersion?: (v: number | null) => void;
  versionContent?: string;
  versionContentLoading?: boolean;
}

export function isTextContent(contentType: string): boolean {
  const ct = contentType.toLowerCase();
  return ct.includes("text/") || ct.includes("html") || ct.includes("svg") ||
    ct.includes("xml") || ct.includes("json") || ct.includes("javascript") ||
    ct.includes("jsx") || ct.includes("markdown");
}
