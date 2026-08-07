import { AlertCircle, Check } from "lucide-react";
import type {
  ConnectionHealth,
  ConnectionOAuthHealthSummary,
} from "@/api/admin/types";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";

// Per-connection health indicators surfaced on the list and detail panes.
// Extracted from ConnectionsPanel.tsx (#766) so the OAuth and gateway
// reachability badges live together, apart from the panel wiring.

// Dot is the leading indicator inside a health badge. It restates the badge's
// variant as a shape, so the state survives a monochrome render.
function Dot({ className }: { className?: string }) {
  return <span aria-hidden className={cn("size-1.5 rounded-full", className)} />;
}

// ConnectionOAuthHealthBadge renders the per-row health indicator
// on the connection list. Visible only when the bulk health hook
// has data AND the connection has OAuth configured. Three states:
//
//   - needs_reauth=true       → danger badge, "needs reauth" tooltip
//   - last refresh failed but not yet terminal → warning badge, code in tooltip
//   - token_acquired && no recent failure → no badge (default)
//
// The operator sees the red dot from the connection list without
// clicking in, addressing the "API calls are silently failing"
// blind spot in the UI.
export function ConnectionOAuthHealthBadge({
  health,
}: {
  health: ConnectionOAuthHealthSummary | undefined;
}) {
  if (!health || !health.has_oauth) return null;
  if (health.needs_reauth) {
    const code = health.idp_error_code;
    const tooltip = code
      ? `Reauth required (${code}). Click in to view details.`
      : "Reauth required. Click in to view details.";
    return (
      <Badge variant="danger" title={tooltip} aria-label={tooltip}>
        <Dot className="bg-destructive" />
        reauth
      </Badge>
    );
  }
  if (health.idp_error_code) {
    // Token still considered valid but the most recent refresh
    // failed transiently. Surface so the operator notices before
    // the access token actually expires.
    const tooltip = `Last refresh failed (${health.idp_error_code}). Retrying.`;
    return (
      <Badge variant="warning" title={tooltip} aria-label={tooltip}>
        <Dot className="bg-amber-500" />
        refresh failing
      </Badge>
    );
  }
  return null;
}

// GatewayHealthBadge renders the runtime reachability of a gateway upstream on
// the connection list. Present only when the backend reports health (gateway
// kinds with a live session). Mirrors the list_connections MCP tool: reachable
// shows green, unreachable shows red with the last error in the tooltip so an
// operator sees a dead upstream from the list without clicking in.
export function GatewayHealthBadge({
  health,
}: {
  health: ConnectionHealth | undefined;
}) {
  if (!health) return null;
  if (health.reachable) {
    const tooltip = health.last_success
      ? `Reachable. Last successful call ${new Date(health.last_success).toLocaleString()}.`
      : "Reachable.";
    return (
      <Badge variant="success" title={tooltip} aria-label={tooltip}>
        <Dot className="bg-emerald-500" />
        reachable
      </Badge>
    );
  }
  const tooltip = health.last_error
    ? `Unreachable: ${health.last_error}`
    : "Unreachable.";
  return (
    <Badge variant="danger" title={tooltip} aria-label={tooltip}>
      <Dot className="bg-destructive" />
      unreachable
    </Badge>
  );
}

// GatewayHealthDetail renders the full reachability detail for a selected
// gateway connection: reachable/unreachable, the last successful call time,
// and the last error. Surfaces in the detail pane so an operator diagnosing a
// failing upstream sees the reason without leaving the connection editor.
export function GatewayHealthDetail({ health }: { health: ConnectionHealth }) {
  return (
    <SectionCard
      title={
        <span className="flex items-center gap-2">
          {health.reachable ? (
            <Check className="size-4 text-emerald-600 dark:text-emerald-400" />
          ) : (
            <AlertCircle className="size-4 text-destructive" />
          )}
          Reachability
        </span>
      }
      action={<GatewayHealthBadge health={health} />}
    >
      <dl className="space-y-2 text-xs">
        <div className="flex justify-between gap-4">
          <dt className="text-muted-foreground">Last successful call</dt>
          <dd className="font-mono">
            {health.last_success
              ? new Date(health.last_success).toLocaleString()
              : "never"}
          </dd>
        </div>
        {health.last_error && (
          <div className="flex justify-between gap-4">
            <dt className="text-muted-foreground">Last error</dt>
            <dd className="font-mono text-destructive break-all text-right">
              {health.last_error}
            </dd>
          </div>
        )}
      </dl>
    </SectionCard>
  );
}
