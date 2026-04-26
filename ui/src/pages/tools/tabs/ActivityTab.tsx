import { ExternalLink } from "lucide-react";
import type { ToolDetail } from "@/api/admin/types";
import { formatDuration } from "@/lib/formatDuration";
import { StatusBadge } from "@/components/cards/StatusBadge";

export function ActivityTab({ detail }: { detail: ToolDetail }) {
  const a = detail.activity;
  const auditHref = `/portal/admin/audit?tool=${encodeURIComponent(detail.name)}`;

  if (!a || a.call_count === 0) {
    return (
      <div className="space-y-3">
        <p className="text-sm text-muted-foreground">
          No calls recorded for this tool in the last{" "}
          {a ? formatWindow(a.window_seconds) : "24 hours"}.
        </p>
        <a
          href={auditHref}
          className="inline-flex items-center gap-1 text-xs text-primary hover:underline"
        >
          Open audit log <ExternalLink className="h-3 w-3" />
        </a>
      </div>
    );
  }

  const successPct = Math.round(a.success_rate * 100);
  const successVariant: "success" | "warning" | "error" =
    a.success_rate >= 0.95 ? "success" : a.success_rate >= 0.8 ? "warning" : "error";

  return (
    <div className="space-y-4">
      <p className="text-xs text-muted-foreground">
        Aggregated over the last {formatWindow(a.window_seconds)}.
      </p>
      <div className="grid grid-cols-3 gap-3">
        <Metric label="Calls" value={a.call_count.toLocaleString()} />
        <Metric
          label="Success rate"
          value={
            <StatusBadge variant={successVariant}>{successPct}%</StatusBadge>
          }
        />
        <Metric label="Avg duration" value={formatDuration(a.avg_duration_ms)} />
      </div>
      <a
        href={auditHref}
        className="inline-flex items-center gap-1 text-xs text-primary hover:underline"
      >
        View full audit log for this tool <ExternalLink className="h-3 w-3" />
      </a>
    </div>
  );
}

function Metric({
  label,
  value,
}: {
  label: string;
  value: React.ReactNode;
}) {
  return (
    <div className="rounded border bg-card p-3">
      <p className="text-[11px] uppercase tracking-wide text-muted-foreground">
        {label}
      </p>
      <p className="mt-1 text-lg font-semibold">{value}</p>
    </div>
  );
}

function formatWindow(seconds: number): string {
  if (seconds <= 0) return "—";
  const hours = Math.round(seconds / 3600);
  if (hours >= 24 && hours % 24 === 0) {
    const days = hours / 24;
    return `${days} day${days === 1 ? "" : "s"}`;
  }
  return `${hours} hour${hours === 1 ? "" : "s"}`;
}
