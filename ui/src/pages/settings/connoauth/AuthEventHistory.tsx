import { useState } from "react";
import { ChevronDown, ChevronRight, History } from "lucide-react";
import { useConnectionAuthEvents } from "@/api/admin/hooks";
import type { ConnectionAuthEvent } from "@/api/admin/types";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { formatRelative, renderDetailHint } from "./format";

// EVENT_LABELS is the operator-facing label for each event type. The
// distinction between "failed (revoked)" and the two "skipped" types matters:
// failed_revoked means the IdP was called and rejected the refresh; the
// skipped types mean the platform reached the verdict without contacting the
// IdP (deadline disclosed by a previous successful refresh, or no refresh
// token was stored).
const EVENT_LABELS: Record<ConnectionAuthEvent["event_type"], string> = {
  connect_started: "Connect started",
  connect_completed: "Connect completed",
  refresh_succeeded: "Refresh succeeded",
  refresh_failed_transient: "Refresh failed (transient)",
  refresh_failed_revoked: "Refresh rejected by IdP",
  refresh_skipped_no_token: "Refresh skipped — no refresh token stored",
  refresh_skipped_expired: "Refresh skipped — IdP-disclosed deadline reached",
  refresh_rotation_persistence_failed: "Rotated token persistence failed",
  token_deleted_revoked: "Token row deleted",
  token_deleted_admin: "Token row deleted — admin",
};

const EVENT_TONE: Record<ConnectionAuthEvent["event_type"], string> = {
  connect_started: "text-muted-foreground",
  connect_completed: "text-emerald-600 dark:text-emerald-400",
  refresh_succeeded: "text-emerald-600 dark:text-emerald-400",
  refresh_failed_transient: "text-amber-600 dark:text-amber-400",
  refresh_failed_revoked: "text-destructive",
  refresh_skipped_no_token: "text-amber-600 dark:text-amber-400",
  refresh_skipped_expired: "text-amber-600 dark:text-amber-400",
  refresh_rotation_persistence_failed: "text-destructive font-medium",
  token_deleted_revoked: "text-destructive",
  token_deleted_admin: "text-muted-foreground",
};

// AuthEventHistory is the collapsible History section under the OAuth status
// card. Renders the most recent lifecycle events so operators can answer "when
// did this connection's token last refresh, and what triggered the previous
// deletion?" without opening pod logs. Hidden by default — most operators
// don't need the detail except when debugging.
export function AuthEventHistory({ kind, name }: { kind: string; name: string }) {
  const [open, setOpen] = useState(false);
  const { data: events, isLoading } = useConnectionAuthEvents(kind, name, open);
  const list = events ?? [];
  return (
    <div className="rounded-md border bg-muted/10 px-2 py-1.5">
      <Button
        type="button"
        variant="ghost"
        size="xs"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        className="w-full justify-start px-0 text-muted-foreground"
      >
        {open ? <ChevronDown /> : <ChevronRight />}
        <History />
        <span>History</span>
        {open && !isLoading && (
          <span className="ml-1 text-muted-foreground/70">({list.length})</span>
        )}
      </Button>
      {open && (
        <div className="mt-2 space-y-1">
          {isLoading && <div className="text-xs text-muted-foreground">Loading…</div>}
          {!isLoading && list.length === 0 && (
            <div className="text-xs text-muted-foreground">
              No events recorded yet for this connection.
            </div>
          )}
          {!isLoading &&
            list.map((ev) => <AuthEventRow key={ev.id} event={ev} />)}
        </div>
      )}
    </div>
  );
}

function AuthEventRow({ event }: { event: ConnectionAuthEvent }) {
  const label = EVENT_LABELS[event.event_type] || event.event_type;
  const tone = EVENT_TONE[event.event_type] || "text-muted-foreground";
  const detail = renderDetailHint(event);
  return (
    <div className="flex items-baseline gap-2 text-xs">
      <span className="w-20 flex-shrink-0 font-mono text-muted-foreground">
        {formatRelative(event.occurred_at)}
      </span>
      <span className={cn("font-medium", tone)}>{label}</span>
      <span className="truncate font-mono text-[11px] text-muted-foreground/70">
        {event.actor}
      </span>
      {detail && <span className="truncate text-muted-foreground/70">{detail}</span>}
    </div>
  );
}
