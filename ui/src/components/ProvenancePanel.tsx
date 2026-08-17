import { useState } from "react";
import {
  Database,
  Globe,
  Search,
  FileText,
  Info,
  Terminal,
  Copy,
  Check,
  History,
  type LucideIcon,
} from "lucide-react";
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
  ProvenanceToolCall,
} from "@/api/portal/types";
import { formatToolName } from "@/lib/formatToolName";
import { formatDuration } from "@/lib/formatDuration";

interface Props {
  provenance: Provenance;
  /**
   * Opens the session these calls belong to. The panel shows the calls the
   * asset captured at the moment it was written; the session holds every call
   * that session made, before and after. Omitted where the reader cannot open
   * it — a session refuses anyone but its own caller and an admin (#1319).
   */
  onOpenSession?: () => void;
}

/** Map tool name prefixes to icons for provenance display. */
const TOOL_ICONS: Record<string, LucideIcon> = {
  trino_: Database,
  datahub_: Search,
  s3_: FileText,
  api_: Globe,
  platform_: Info,
};

function getToolIcon(toolName: string): LucideIcon {
  for (const [prefix, icon] of Object.entries(TOOL_ICONS)) {
    if (toolName.startsWith(prefix)) return icon;
  }
  return Terminal;
}

const KIND_LABELS: Record<string, string> = {
  sql: "SQL",
  api: "API",
  tool: "Tool",
};

function relativeTime(timestamp: string): string {
  const now = Date.now();
  const then = new Date(timestamp).getTime();
  const diff = Math.max(0, now - then);
  const seconds = Math.floor(diff / 1000);
  if (seconds < 60) return "just now";
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes} min ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.floor(hours / 24);
  return `${days}d ago`;
}

/** The one line that says what a call did: the statement, the request, or what it addressed. */
function callSummary(call: ProvenanceCall): string {
  if (call.kind === "sql") return call.statement ?? "";
  if (call.kind === "api") {
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

function truncate(text: string, max = 120): string {
  return text.length > max ? text.slice(0, max) + "..." : text;
}

/** Copy control shared by the call detail and the call reference. */
function CopyButton({ text, label }: { text: string; label: string }) {
  const [copied, setCopied] = useState(false);

  const handleCopy = () => {
    const done = () => {
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    };
    const writeFallback = () => {
      const el = document.createElement("textarea");
      el.value = text;
      document.body.appendChild(el);
      el.select();
      document.execCommand("copy");
      document.body.removeChild(el);
      done();
    };

    if (navigator.clipboard?.writeText) {
      navigator.clipboard.writeText(text).then(done, writeFallback);
    } else {
      writeFallback();
    }
  };

  return (
    <Button
      type="button"
      variant="ghost"
      size="xs"
      onClick={handleCopy}
      className="text-muted-foreground"
      title={label}
      aria-label={label}
    >
      {copied ? (
        <>
          <Check className="text-emerald-600 dark:text-emerald-400" />
          Copied
        </>
      ) : (
        <>
          <Copy />
          Copy
        </>
      )}
    </Button>
  );
}

function CallCard({
  call,
  onClick,
}: {
  call: ProvenanceCall;
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
          <Badge variant="info" title="The agent named these calls">
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

export function ProvenancePanel({ provenance, onOpenSession }: Props) {
  const captures = provenance.captures ?? [];
  const legacyCalls = provenance.tool_calls ?? [];
  const [selected, setSelected] = useState<ProvenanceCall | null>(null);

  if (captures.length === 0) {
    if (legacyCalls.length > 0) {
      return (
        <LegacyProvenance calls={legacyCalls} onOpenSession={onOpenSession} />
      );
    }
    return <NoProvenance onOpenSession={onOpenSession} />;
  }

  const total = captures.reduce((n, c) => n + (c.calls?.length ?? 0), 0);

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-medium">Provenance</h3>
        <span className="text-xs text-muted-foreground">
          {total} {total === 1 ? "call" : "calls"}
        </span>
      </div>

      {captures.map((capture, ci) => (
        <div key={ci} className="space-y-2">
          <CaptureHeading capture={capture} />
          {(capture.calls ?? []).map((call, i) => (
            <CallCard
              key={call.event_id ?? `${ci}-${i}`}
              call={call}
              onClick={() => setSelected(call)}
            />
          ))}
          {capture.truncated && (
            <p className="text-[11px] text-muted-foreground">
              {capture.explicit
                ? "Some cited calls were not found and are not recorded."
                : "More calls were made than this capture records."}
            </p>
          )}
        </div>
      ))}

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
 * Assets written before #1320 carry a flat list of tool calls with their raw
 * parameters and no outcome, duration, or identity. They are still shown, as
 * what they are.
 */
function LegacyProvenance({
  calls,
  onOpenSession,
}: {
  calls: ProvenanceToolCall[];
  onOpenSession?: () => void;
}) {
  const [selected, setSelected] = useState<ProvenanceToolCall | null>(null);

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-medium">Provenance</h3>
        <span className="text-xs text-muted-foreground">
          {calls.length} {calls.length === 1 ? "call" : "calls"}
        </span>
      </div>

      <div className="space-y-2">
        {calls.map((call, i) => {
          const Icon = getToolIcon(call.tool_name);
          return (
            <button
              type="button"
              key={i}
              onClick={() => setSelected(call)}
              className="w-full text-left rounded-md border bg-card p-3 transition-colors hover:bg-muted/50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            >
              <div className="flex items-start gap-2.5">
                <div className="mt-0.5 rounded bg-muted p-1.5">
                  <Icon className="h-3.5 w-3.5 text-muted-foreground" />
                </div>
                <div className="min-w-0 flex-1">
                  <div className="flex items-center justify-between gap-2">
                    <span className="truncate text-sm font-medium">
                      {formatToolName(call.tool_name)}
                    </span>
                    <span
                      className="shrink-0 text-[11px] text-muted-foreground"
                      title={new Date(call.timestamp).toLocaleString()}
                    >
                      {relativeTime(call.timestamp)}
                    </span>
                  </div>
                  {legacySummary(call) && (
                    <p className="mt-0.5 truncate text-xs text-muted-foreground font-mono">
                      {truncate(legacySummary(call))}
                    </p>
                  )}
                </div>
              </div>
            </button>
          );
        })}
      </div>

      {onOpenSession && <OpenSessionButton onClick={onOpenSession} />}

      <Dialog
        open={selected !== null}
        onOpenChange={(open) => {
          if (!open) setSelected(null);
        }}
      >
        <DialogContent showCloseButton={false}>
          <DialogHeader>
            <DialogTitle>
              {selected ? formatToolName(selected.tool_name) : ""}
            </DialogTitle>
            <DialogDescription className="text-xs">
              {selected
                ? `${selected.tool_name} · ${new Date(selected.timestamp).toLocaleString()}`
                : ""}
            </DialogDescription>
          </DialogHeader>
          <div>
            <div className="mb-1.5 flex items-center justify-between">
              <p className="text-xs font-medium text-muted-foreground">
                Parameters
              </p>
              <CopyButton
                text={legacyDetail(selected)}
                label="Copy parameters"
              />
            </div>
            <pre className="max-h-96 overflow-auto rounded-md bg-muted p-3 text-xs font-mono whitespace-pre-wrap break-words">
              {legacyDetail(selected)}
            </pre>
          </div>
          <DialogFooter>
            <DialogClose asChild>
              <Button type="button" variant="secondary">
                Close
              </Button>
            </DialogClose>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}

function legacySummary(call: ProvenanceToolCall): string {
  const params = call.parameters;
  if (!params) return "";
  for (const key of ["sql", "query", "urn", "table", "path", "bucket"]) {
    const value = params[key];
    if (typeof value === "string" && value) return value;
  }
  return "";
}

function legacyDetail(call: ProvenanceToolCall | null): string {
  if (!call?.parameters || Object.keys(call.parameters).length === 0) {
    return "(no parameters)";
  }
  if (typeof call.parameters.sql === "string") return call.parameters.sql;
  return JSON.stringify(call.parameters, null, 2);
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

/** Walks from what the asset captured to the whole session that made it. */
function OpenSessionButton({ onClick }: { onClick: () => void }) {
  return (
    <Button
      type="button"
      variant="outline"
      size="xs"
      onClick={onClick}
      className="w-full text-muted-foreground"
    >
      <History />
      Open session
    </Button>
  );
}
