import { FileUp, LayoutGrid, type LucideIcon } from "lucide-react";
import type { TableSourceKind } from "@/api/tables/types";

// A scratch table is always over a file the platform already holds, and there
// are two ways a file gets there: somebody uploaded it as a managed resource,
// or the platform wrote it as a portal asset. The listing spans both, so the
// per-kind facts a row needs -- what to call the kind, which icon it carries,
// and where the portal opens it -- live here rather than in three components
// each making the mapping again.

interface SourceKindInfo {
  label: string;
  icon: LucideIcon;
  /** path is where the portal opens a source of this kind. */
  path: (id: string) => string;
}

const KINDS: Record<TableSourceKind, SourceKindInfo> = {
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
};

/** sourceKindInfo describes one source kind, or undefined for an unknown one. */
export function sourceKindInfo(kind: string): SourceKindInfo | undefined {
  return KINDS[kind as TableSourceKind];
}

/** sourceKindLabel names a source kind for a reader. */
export function sourceKindLabel(kind: string): string {
  return sourceKindInfo(kind)?.label ?? kind;
}

/**
 * sourcePath is where the portal opens a source, or null when there is nowhere
 * to send the reader: a kind the portal has no page for, or a record that is
 * gone. A link to a page that answers "no such file" is worse than no link.
 */
export function sourcePath(kind: string, id: string, missing: boolean): string | null {
  if (missing) return null;
  const info = sourceKindInfo(kind);
  return info ? info.path(id) : null;
}
