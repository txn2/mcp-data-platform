import { useRef } from "react";
import { useVirtualizer } from "@tanstack/react-virtual";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import { formatBytes } from "@/lib/format";
import { WebImageBadge } from "../../parts/badges";
import { targetPath, type BulkItem } from "../../bulk/plan";
import { OUTCOME_LABELS, type ItemStatus } from "../../bulk/progress";

/** Row height in pixels; fixed so the virtualizer sizes without measuring. */
const ROW_HEIGHT = 64;

/** The list's viewport; a batch of thousands scrolls inside it. */
const VIEWPORT_HEIGHT = 320;

/**
 * The files of a bulk upload, one row each: the name it will be listed under
 * (editable until the batch runs), where it will be filed, and what happened to
 * it (#1862). Virtualized, because the motivating batch is a product image
 * library in the thousands.
 */
export function BulkFileList({
  items,
  base,
  statusOf,
  onRename,
  editable,
}: {
  items: BulkItem[];
  base: string;
  statusOf: (key: string) => ItemStatus;
  onRename: (key: string, name: string) => void;
  editable: boolean;
}) {
  const parentRef = useRef<HTMLDivElement>(null);
  const virtualizer = useVirtualizer({
    count: items.length,
    getScrollElement: () => parentRef.current,
    estimateSize: () => ROW_HEIGHT,
    overscan: 12,
    // A starting size, so the first rows render before layout has measured the
    // viewport (and at all where nothing measures, as under a test DOM).
    initialRect: { width: 640, height: VIEWPORT_HEIGHT },
  });

  if (items.length === 0) {
    return (
      <p className="text-sm text-muted-foreground" data-testid="bulk-empty">
        No files chosen yet.
      </p>
    );
  }

  return (
    <div
      ref={parentRef}
      className="overflow-auto rounded-md border"
      style={{ maxHeight: VIEWPORT_HEIGHT }}
      data-testid="bulk-file-list"
    >
      <div style={{ height: virtualizer.getTotalSize(), position: "relative" }}>
        {virtualizer.getVirtualItems().map((row) => {
          const item = items[row.index] as BulkItem;
          return (
            <div
              key={item.key}
              className="absolute inset-x-0 border-b px-3 py-2"
              style={{ height: ROW_HEIGHT, transform: `translateY(${row.start}px)` }}
            >
              <BulkRow
                item={item}
                folder={targetPath(base, item.relDir)}
                status={statusOf(item.key)}
                onRename={onRename}
                editable={editable}
              />
            </div>
          );
        })}
      </div>
    </div>
  );
}

function BulkRow({
  item,
  folder,
  status,
  onRename,
  editable,
}: {
  item: BulkItem;
  folder: string;
  status: ItemStatus;
  onRename: (key: string, name: string) => void;
  editable: boolean;
}) {
  const settled = status.state === "done";
  return (
    <div className="flex h-full items-center gap-3" data-testid="bulk-row">
      <div className="min-w-0 flex-1 space-y-1">
        <Input
          aria-label={`Display name for ${item.file.name}`}
          value={item.displayName}
          onChange={(e) => onRename(item.key, e.target.value)}
          disabled={!editable || settled || item.problem !== null}
          className="h-7 text-sm"
        />
        <p className="truncate text-xs text-muted-foreground" title={`${folder}/${item.filename}`}>
          {item.filename ? `${folder}/${item.filename}` : item.file.name} · {formatBytes(item.file.size)}
        </p>
      </div>
      {item.webImage && <WebImageBadge />}
      <StatusCell status={item.problem ? { state: "refused", error: item.problem } : status} />
    </div>
  );
}

/** What happened to one file, with the server's reason for a failure. */
function StatusCell({ status }: { status: ItemStatus }) {
  switch (status.state) {
    case "ready":
      return <span className="w-28 shrink-0 text-right text-xs text-muted-foreground">Ready</span>;
    case "sending":
      return (
        <span className="w-28 shrink-0 text-right text-xs text-muted-foreground" data-testid="bulk-progress">
          Uploading {Math.round(status.progress * 100)}%
        </span>
      );
    case "done":
      return (
        <Badge variant={status.outcome === "unchanged" ? "muted" : "info"} className="shrink-0 px-1.5" data-testid="bulk-outcome">
          {OUTCOME_LABELS[status.outcome]}
        </Badge>
      );
    default:
      return (
        <span className="w-40 shrink-0 text-right text-xs text-destructive" title={status.error} data-testid="bulk-failure">
          <span className="font-medium">{status.state === "failed" ? "Failed" : "Not sent"}</span>
          <span className="line-clamp-2 block">{status.error}</span>
        </span>
      );
  }
}
