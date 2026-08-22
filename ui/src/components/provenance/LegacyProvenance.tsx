import { useState } from "react";
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import type { ProvenanceToolCall } from "@/api/portal/types";
import { formatToolName } from "@/lib/formatToolName";
import {
  CopyButton,
  OpenSessionButton,
  getToolIcon,
  relativeTime,
  truncate,
} from "./parts";

/**
 * Assets written before #1320 carry a flat list of tool calls with their raw
 * parameters and no outcome, duration, or identity. They are still shown, as
 * what they are.
 */
export function LegacyProvenance({
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
