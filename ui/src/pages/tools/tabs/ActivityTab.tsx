import { ExternalLink } from "lucide-react";
import type { ToolDetail } from "@/api/admin/types";
import { formatDuration } from "@/lib/formatDuration";
import { StatCard } from "@/components/cards/StatCard";
import { StatusBadge } from "@/components/cards/StatusBadge";
import { Button } from "@/components/ui/button";

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
        <AuditLogLink href={auditHref}>Open audit log</AuditLogLink>
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
        <StatCard label="Calls" value={a.call_count.toLocaleString()} />
        {/* The rate is the one figure that carries a verdict, so it reads as a
            status pill rather than as another number. */}
        <StatCard
          label="Success rate"
          value={<StatusBadge variant={successVariant}>{successPct}%</StatusBadge>}
        />
        <StatCard label="Avg duration" value={formatDuration(a.avg_duration_ms)} />
      </div>
      <AuditLogLink href={auditHref}>View full audit log for this tool</AuditLogLink>
    </div>
  );
}

function AuditLogLink({ href, children }: { href: string; children: React.ReactNode }) {
  return (
    <Button asChild variant="link" size="xs" className="px-0">
      <a href={href}>
        {children} <ExternalLink />
      </a>
    </Button>
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
