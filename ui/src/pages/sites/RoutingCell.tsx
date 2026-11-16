import type { Routing } from "@/api/portal/hooks/sites";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { when } from "./siteFormat";

// RoutingCell is one hostname's Site routing status (#2659), the one component
// every address and domain row shows it with: pending until the routing script
// answers, then ready or failed with the script's message, rendered as text.
// When the script itself failed, an owner reads one sentence; an administrator
// also reads the run's error with a link to the run. Retry is offered on a
// failed row to anyone who can see it; a retry the platform refuses (asked
// again just after a run started) says why under it.

const LABEL: Record<Routing["status"], string> = {
  pending: "Pending",
  ready: "Ready",
  failed: "Failed",
};
const VARIANT: Record<Routing["status"], "muted" | "success" | "warning"> = {
  pending: "muted",
  ready: "success",
  failed: "warning",
};

export function RoutingCell({
  routing,
  onRetry,
  retrying,
  retryError,
}: {
  routing: Routing;
  onRetry?: () => void;
  retrying?: boolean;
  retryError?: string;
}) {
  const message =
    routing.message ||
    (routing.status === "pending" ? "Waiting for the routing script" : "");
  const checked = routing.checked_at ? when(routing.checked_at) : "";
  return (
    <div className="flex flex-col gap-2" data-testid="routing-cell">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0 space-y-0.5">
          <div className="flex flex-wrap items-baseline gap-2">
            <Badge variant={VARIANT[routing.status]}>
              {LABEL[routing.status]}
            </Badge>
            {message && <span className="break-words text-sm">{message}</span>}
          </div>
          {checked && (
            <div className="text-xs text-muted-foreground">
              checked {checked}
            </div>
          )}
        </div>
        {routing.status === "failed" && onRetry && (
          <Button
            size="sm"
            variant="outline"
            onClick={onRetry}
            disabled={retrying}
          >
            Retry
          </Button>
        )}
      </div>
      <RetryRefusal routing={routing} why={retryError} />
      {routing.run_error && <RunError routing={routing} />}
    </div>
  );
}

// RetryRefusal is why a retry of a failed row was refused.
function RetryRefusal({ routing, why }: { routing: Routing; why?: string }) {
  if (routing.status !== "failed" || !why) return null;
  return (
    <div className="text-xs text-destructive" role="alert">
      {why}
    </div>
  );
}

// RunError is the failing run's own error and where to read it, which only an
// administrator is sent.
function RunError({ routing }: { routing: Routing }) {
  return (
    <div className="space-y-1 rounded-md border border-amber-200 bg-amber-50 px-2.5 py-2 text-xs dark:border-amber-500/30 dark:bg-amber-500/10">
      <div className="font-medium text-amber-900 dark:text-amber-200">
        Seen by administrators only
      </div>
      <code className="block whitespace-pre-wrap break-words text-amber-900 dark:text-amber-200">
        {routing.run_error}
      </code>
      <div className="flex flex-wrap gap-3">
        {routing.run_id && routing.run_script_id && (
          <a
            className="text-primary underline-offset-4 hover:underline"
            href={`/portal/automations/${routing.run_script_id}/runs/${routing.run_id}`}
          >
            Open the run
          </a>
        )}
        <a
          className="text-primary underline-offset-4 hover:underline"
          href="/portal/admin/settings/integrations"
        >
          Integrations settings
        </a>
      </div>
    </div>
  );
}
