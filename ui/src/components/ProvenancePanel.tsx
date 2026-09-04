import { useState } from "react";
import { ChevronDown, ChevronRight } from "lucide-react";
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import type {
  Provenance,
  ProvenanceCall,
  ProvenanceCapture,
} from "@/api/portal/types";
import { formatToolName } from "@/lib/formatToolName";
import { formatDuration } from "@/lib/formatDuration";
import {
  PROVENANCE_PAGE_SIZE,
  useAssetProvenance,
} from "@/api/portal/hooks/provenance";
import { LegacyProvenance } from "./provenance/LegacyProvenance";
import {
  CopyButton,
  OpenSessionButton,
  getToolIcon,
  relativeTime,
  truncate,
} from "./provenance/parts";

interface Props {
  /** What the asset read carried. Absent on an asset that recorded nothing. */
  provenance?: Provenance;
  /**
   * The asset these captures belong to. Present, the panel can page the
   * captures the asset read left out (#1623); absent, it shows what it was
   * handed and offers no control to load more.
   */
  assetId?: string;
  /**
   * Opens the session these calls belong to. The panel shows the calls the
   * asset captured at the moment it was written; the session holds every call
   * that session made, before and after. Omitted where the reader cannot open
   * it — a session refuses anyone but its own caller and an admin (#1319).
   */
  onOpenSession?: () => void;
}

const KIND_LABELS: Record<string, string> = {
  sql: "SQL",
  api: "API",
  tool: "Tool",
};

/** The one line that says what a call did: the statement, the request, or what it addressed. */
function callSummary(call: ProvenanceCall): string {
  if (call.kind === "sql") return call.statement ?? "";
  if (call.kind === "api") {
    // The recorded request says which of several calls to one operation this
    // was; the method and path are what a capture taken before #1423 holds.
    if (call.request) return call.request;
    const request = [call.method, call.path].filter(Boolean).join(" ");
    return request || call.operation_id || "";
  }
  return call.summary ?? "";
}

/** The full text a reader copies: the statement for a query, the request otherwise. */
function callDetail(call: ProvenanceCall): string {
  const summary = callSummary(call);
  if (summary) return summary;
  return call.operation_id || call.tool;
}

function CallCard({
  call,
  named,
  onClick,
}: {
  call: ProvenanceCall;
  /**
   * Show that this call was named as a source. Only passed inside a capture the
   * caller did not name wholesale, where one call standing out from the window
   * says something: an export's own statement is the file's content, while the
   * calls around it were merely in scope (#1353).
   */
  named?: boolean;
  onClick: () => void;
}) {
  const Icon = getToolIcon(call.tool);
  const summary = callSummary(call);
  const failed = call.outcome === "error";

  return (
    <button
      type="button"
      onClick={onClick}
      className="w-full text-left rounded-md border bg-card p-3 transition-colors hover:bg-muted/50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
    >
      <div className="flex items-start gap-2.5">
        <div className="mt-0.5 rounded bg-muted p-1.5">
          <Icon className="h-3.5 w-3.5 text-muted-foreground" />
        </div>
        <div className="min-w-0 flex-1">
          <div className="flex items-center justify-between gap-2">
            <span className="flex min-w-0 items-center gap-1.5">
              <Badge variant="muted" className="shrink-0">
                {KIND_LABELS[call.kind] ?? call.kind}
              </Badge>
              <span className="truncate text-sm font-medium">
                {formatToolName(call.tool)}
              </span>
              {failed && (
                <Badge variant="danger" className="shrink-0">
                  Failed
                </Badge>
              )}
              {named && (
                <Badge
                  variant="info"
                  className="shrink-0"
                  title="This call was named as a source of the write"
                >
                  Source
                </Badge>
              )}
            </span>
            <span
              className="shrink-0 text-[11px] text-muted-foreground"
              title={new Date(call.timestamp).toLocaleString()}
            >
              {relativeTime(call.timestamp)}
            </span>
          </div>
          {summary && (
            <p className="mt-0.5 truncate text-xs text-muted-foreground font-mono">
              {truncate(summary)}
            </p>
          )}
          {call.purpose && (
            <p className="mt-1 line-clamp-2 text-xs text-muted-foreground italic">
              {call.purpose}
            </p>
          )}
          <div className="mt-1 flex flex-wrap gap-x-2 text-[11px] text-muted-foreground">
            {call.connection && <span>{call.connection}</span>}
            {call.duration_ms !== undefined && call.duration_ms > 0 && (
              <span>{formatDuration(call.duration_ms)}</span>
            )}
          </div>
        </div>
      </div>
    </button>
  );
}

function CallDetailModal({
  call,
  open,
  onOpenChange,
}: {
  call: ProvenanceCall | null;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  if (!call) return null;
  const Icon = getToolIcon(call.tool);
  const detail = callDetail(call);
  const isStatement = call.kind === "sql" && Boolean(call.statement);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent showCloseButton={false}>
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <Icon className="h-4 w-4 text-muted-foreground" />
            {formatToolName(call.tool)}
          </DialogTitle>
          <DialogDescription className="text-xs">
            {call.tool} &middot; {new Date(call.timestamp).toLocaleString()}
            {call.connection && <> &middot; {call.connection}</>}
            {call.duration_ms !== undefined && call.duration_ms > 0 && (
              <> &middot; {formatDuration(call.duration_ms)}</>
            )}
          </DialogDescription>
        </DialogHeader>

        {call.purpose && (
          <div>
            <p className="mb-1 text-xs font-medium text-muted-foreground">
              Stated purpose
            </p>
            <p className="text-sm">{call.purpose}</p>
          </div>
        )}

        {call.outcome === "error" && (
          <div>
            <p className="mb-1 text-xs font-medium text-muted-foreground">
              Outcome
            </p>
            <p className="text-sm text-red-700 dark:text-red-300">
              Failed{call.error ? `: ${call.error}` : ""}
            </p>
          </div>
        )}

        <div>
          <div className="mb-1.5 flex items-center justify-between">
            <p className="text-xs font-medium text-muted-foreground">
              {isStatement ? "SQL Query" : "Request"}
            </p>
            <CopyButton
              text={detail}
              label={isStatement ? "Copy SQL query" : "Copy request"}
            />
          </div>
          <pre className="max-h-96 overflow-auto rounded-md bg-muted p-3 text-xs font-mono whitespace-pre-wrap break-words">
            {detail}
          </pre>
        </div>

        {call.event_id && (
          <div>
            <div className="mb-1.5 flex items-center justify-between">
              <p className="text-xs font-medium text-muted-foreground">
                Call reference
              </p>
              <CopyButton
                text={`mcp:call:${call.event_id}`}
                label="Copy call reference"
              />
            </div>
            <p className="font-mono text-xs break-all text-muted-foreground">
              mcp:call:{call.event_id}
            </p>
          </div>
        )}

        <DialogFooter>
          <DialogClose asChild>
            <Button type="button" variant="secondary">
              Close
            </Button>
          </DialogClose>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/** The heading over one capture: which write it belongs to, and how it was decided. */
function CaptureHeading({ capture }: { capture: ProvenanceCapture }) {
  const parts = [formatToolName(capture.tool)];
  if (capture.captured_at) parts.push(relativeTime(capture.captured_at));

  return (
    <div className="flex items-center justify-between gap-2">
      <span className="flex items-center gap-1.5 text-xs font-medium">
        {capture.version ? `Version ${capture.version}` : "Capture"}
        {capture.explicit && (
          <Badge variant="info" title="The agent named these calls as the sources of this write">
            Cited
          </Badge>
        )}
      </span>
      <span className="text-[11px] text-muted-foreground">
        {parts.join(" · ")}
      </span>
    </div>
  );
}

/** One capture's calls, and what the capture says about the calls it left out. */
function CaptureCalls({
  capture,
  onSelect,
}: {
  capture: ProvenanceCapture;
  onSelect: (call: ProvenanceCall) => void;
}) {
  return (
    <>
      {(capture.calls ?? []).map((call, i) => (
        <CallCard
          key={call.event_id ?? `call-${i}`}
          call={call}
          named={!capture.explicit && call.cited}
          onClick={() => onSelect(call)}
        />
      ))}
      {capture.truncated && (
        <p className="text-[11px] text-muted-foreground">
          {capture.explicit
            ? "Some cited calls were not found and are not recorded."
            : "More calls were made than this capture records."}
        </p>
      )}
    </>
  );
}

/** An earlier capture, shown as its heading alone until a reader opens it. */
function CollapsedCapture({
  capture,
  onSelect,
}: {
  capture: ProvenanceCapture;
  onSelect: (call: ProvenanceCall) => void;
}) {
  const [open, setOpen] = useState(false);
  const Chevron = open ? ChevronDown : ChevronRight;

  return (
    <div className="space-y-2">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        className="flex w-full items-center gap-1.5 rounded-sm text-left hover:text-foreground/80 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
      >
        <Chevron className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
        <div className="min-w-0 flex-1">
          <CaptureHeading capture={capture} />
        </div>
      </button>
      {open && <CaptureCalls capture={capture} onSelect={onSelect} />}
    </div>
  );
}

/**
 * Every capture but the newest, behind one disclosure, each opening on its own.
 *
 * One capture is written per content write, so an asset a scheduled script
 * refreshes hourly carries one an hour; rendering them all expanded makes the
 * panel as long as the asset's history (#1422). The captures arrive here
 * already reversed, newest first, each paired with its position in the
 * unreversed list. That position is the key: it does not move when a capture
 * is appended, whereas a position in the reversed list shifts by one and would
 * hand a reader's open disclosure to the capture that took its place.
 */
function EarlierCaptures({
  captures,
  unloaded,
  loading,
  onLoadMore,
  onSelect,
}: {
  captures: { capture: ProvenanceCapture; index: number }[];
  /** How many older captures the asset holds that this panel has not read. */
  unloaded: number;
  loading: boolean;
  onLoadMore?: () => void;
  onSelect: (call: ProvenanceCall) => void;
}) {
  const [open, setOpen] = useState(false);
  if (captures.length === 0 && unloaded === 0) return null;

  const shown = captures.length + unloaded;
  const label = `${shown} earlier ${shown === 1 ? "capture" : "captures"}`;
  const Chevron = open ? ChevronDown : ChevronRight;

  return (
    <div className="space-y-2">
      <Button
        type="button"
        variant="link"
        size="xs"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        className="px-0"
      >
        <Chevron />
        {label}
      </Button>
      {open && (
        <div className="space-y-2 border-l pl-2.5">
          {captures.map(({ capture, index }) => (
            <CollapsedCapture
              key={index}
              capture={capture}
              onSelect={onSelect}
            />
          ))}
          {unloaded > 0 && onLoadMore && (
            <Button
              type="button"
              variant="link"
              size="xs"
              className="px-0"
              disabled={loading}
              onClick={onLoadMore}
            >
              {loading
                ? "Loading older captures…"
                : `Load ${Math.min(unloaded, PROVENANCE_PAGE_SIZE)} older ${
                    Math.min(unloaded, PROVENANCE_PAGE_SIZE) === 1
                      ? "capture"
                      : "captures"
                  }`}
            </Button>
          )}
        </div>
      )}
    </div>
  );
}

/** How many calls the asset's whole history records, expanded or not. */
function countCalls(captures: ProvenanceCapture[]): number {
  return captures.reduce((n, c) => n + (c.calls?.length ?? 0), 0);
}

/**
 * The captures the panel has in hand and the ones it can still fetch.
 *
 * An asset read carries only the newest of an asset's captures, because a
 * capture is appended on every write and nothing bounds them (#1623). This puts
 * the ones it carried and the pages a reader has asked for into one
 * newest-first list, and says how many are still unread.
 *
 * The index paired with each capture is its position counting from the oldest
 * the asset holds. That position does not move when a capture is appended,
 * whereas a position in the newest-first list shifts by one and would hand a
 * reader's open disclosure to the capture that took its place.
 */
function useCaptureWindow(provenance: Provenance, assetId?: string) {
  // The older captures are read only when a reader asks for them. Most never
  // do: the newest capture is what the panel leads with, and the asset read
  // already carries it.
  const [wantOlder, setWantOlder] = useState(false);
  const inline = provenance.captures ?? [];
  const heldTotal = provenance.captures_total ?? inline.length;
  const pages = useAssetProvenance(
    assetId,
    inline.length,
    wantOlder && heldTotal > inline.length,
  );

  const inlineBase = heldTotal - inline.length;
  const fetched = (pages.data?.pages ?? []).flatMap((page) =>
    page.captures.map((capture, i) => ({
      capture,
      index: heldTotal - 1 - (page.offset + i),
    })),
  );

  return {
    inline,
    heldTotal,
    // A capture is appended per write, so the last one the asset read carries
    // is the newest. Everything before it is reversed, which puts the whole
    // list in newest-first order rather than making a reader scroll to the
    // current state.
    earlier: [
      ...inline
        .slice(0, -1)
        .map((capture, index) => ({ capture, index: inlineBase + index }))
        .reverse(),
      ...fetched,
    ],
    unloaded: Math.max(inlineBase - fetched.length, 0),
    loadedCalls: countCalls([
      ...inline,
      ...fetched.map(({ capture }) => capture),
    ]),
    loading: pages.isFetching,
    loadMore: () => {
      if (wantOlder) {
        void pages.fetchNextPage();
        return;
      }
      setWantOlder(true);
    },
  };
}

/** What the panel writes beside its heading: what is loaded, or what it says. */
function windowLabel(unloaded: number, heldTotal: number, calls: number) {
  if (unloaded > 0) {
    return `${heldTotal - unloaded} of ${heldTotal} captures`;
  }
  return `${calls} ${calls === 1 ? "call" : "calls"}`;
}

export function ProvenancePanel({
  provenance = {},
  assetId,
  onOpenSession,
}: Props) {
  const [selected, setSelected] = useState<ProvenanceCall | null>(null);
  const shown = useCaptureWindow(provenance, assetId);
  const legacyCalls = provenance.tool_calls ?? [];

  if (shown.inline.length === 0) {
    if (legacyCalls.length > 0) {
      return (
        <LegacyProvenance calls={legacyCalls} onOpenSession={onOpenSession} />
      );
    }
    return <NoProvenance onOpenSession={onOpenSession} />;
  }

  const newest = shown.inline[shown.inline.length - 1]!;

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-medium">Provenance</h3>
        <span className="text-xs text-muted-foreground">
          {windowLabel(shown.unloaded, shown.heldTotal, shown.loadedCalls)}
        </span>
      </div>

      <div className="space-y-2">
        <CaptureHeading capture={newest} />
        <CaptureCalls capture={newest} onSelect={setSelected} />
      </div>

      <EarlierCaptures
        captures={shown.earlier}
        unloaded={shown.unloaded}
        loading={shown.loading}
        onLoadMore={assetId ? shown.loadMore : undefined}
        onSelect={setSelected}
      />

      {onOpenSession && <OpenSessionButton onClick={onOpenSession} />}

      <CallDetailModal
        call={selected}
        open={selected !== null}
        onOpenChange={(open) => {
          if (!open) setSelected(null);
        }}
      />
    </div>
  );
}

/**
 * An asset that captured no calls. It still came from a session, and that
 * session is where the calls this asset does not carry are recorded — so this
 * is exactly where the walk to it matters most.
 */
function NoProvenance({ onOpenSession }: { onOpenSession?: () => void }) {
  return (
    <div className="space-y-3">
      <p className="text-sm text-muted-foreground">
        No provenance data available.
      </p>
      {onOpenSession && <OpenSessionButton onClick={onOpenSession} />}
    </div>
  );
}
