import { FileUp, LayoutGrid, Webhook, type LucideIcon } from "lucide-react";
import type { ScratchSourceKind } from "@/api/tables/types";

// A scratch table is over a file the platform already holds -- one somebody
// uploaded as a managed resource, or one the platform wrote as a portal asset
// -- or it is the table of an inbound webhook source (#1870). The listing spans
// all three, so the per-kind facts a row needs -- what to call the kind, which
// icon it carries, and where the portal opens it -- live here rather than in
// three components each making the mapping again.

interface SourceKindInfo {
  label: string;
  icon: LucideIcon;
  /** path is where the portal opens a source of this kind. */
  path: (id: string) => string;
  /** adminOnly is a source whose page only an administrator can open. */
  adminOnly?: boolean;
}

const KINDS: Record<ScratchSourceKind, SourceKindInfo> = {
  resource: {
    label: "Resource",
    icon: FileUp,
    path: (id) => `/resources/${encodeURIComponent(id)}`,
  },
  asset: {
    label: "Asset",
    icon: LayoutGrid,
    path: (id) => `/assets/${encodeURIComponent(id)}`,
  },
  webhook: {
    label: "Webhook source",
    icon: Webhook,
    path: (id) => `/admin/webhooks/${encodeURIComponent(id)}`,
    adminOnly: true,
  },
};

/** sourceKindInfo describes one source kind, or undefined for an unknown one. */
export function sourceKindInfo(kind: string): SourceKindInfo | undefined {
  return KINDS[kind as ScratchSourceKind];
}

/** sourceKindLabel names a source kind for a reader. */
export function sourceKindLabel(kind: string): string {
  return sourceKindInfo(kind)?.label ?? kind;
}

/**
 * sourcePath is where the portal opens a source, or null when there is nowhere
 * to send the reader: a kind the portal has no page for, a record that is
 * gone, or a page only an administrator can open. A link to a page that
 * answers "no such file" or refuses the reader is worse than no link.
 */
export function sourcePath(kind: string, id: string, missing: boolean, isAdmin = false): string | null {
  if (missing) return null;
  const info = sourceKindInfo(kind);
  if (!info || (info.adminOnly && !isAdmin)) return null;
  return info.path(id);
}
