import type { BreakdownEntry } from "@/api/admin/types";
import { ChartSkeleton } from "@/components/charts/ChartSkeleton";

// What each failure category is called on the page (#2795). A category the
// platform adds later shows under its own name until it is named here.
export const failureCategoryNames: Record<string, string> = {
  schema_required: "Table columns not read first",
  search_required: "Search not called first",
  purpose_required: "No purpose given",
  session_expired: "Session expired",
  session_required: "No session",
  invalid_arguments: "Arguments the tool does not take",
  missing_required_parameter: "A required argument missing",
  authorization_denied: "Not allowed",
  authentication_failed: "Not signed in",
  rate_limited: "Rate limited",
  user_declined: "Declined by the person",
  prompt_unanswered: "Question left unanswered",
  input_required: "Waiting for the person",
  not_found: "Not found",
  tool_error: "Tool error",
  upstream_4xx: "Refused by the upstream",
  upstream_5xx: "Upstream failed",
  internal: "Platform error",
  "": "No category recorded",
};

// Where a call came from.
export const sourceNames: Record<string, string> = {
  mcp: "Agent sessions",
  script: "Script runs",
  portal: "Portal",
  endpoint: "Endpoints",
  rest: "REST",
  admin: "Admin",
  "": "Not recorded",
};

/**
 * FailuresPanel is the failed calls of the page's time range counted three
 * ways (#2795): by the category each was refused or failed with, by where
 * the call came from, and by persona. Every count is exact over the range.
 */
export function FailuresPanel({
  byCategory,
  bySource,
  byPersona,
  isLoading,
}: {
  byCategory: BreakdownEntry[] | undefined;
  bySource: BreakdownEntry[] | undefined;
  byPersona: BreakdownEntry[] | undefined;
  isLoading: boolean;
}) {
  if (isLoading || !byCategory || !bySource || !byPersona) return <ChartSkeleton height={160} />;
  const total = byCategory.reduce((n, d) => n + d.count, 0);
  if (total === 0) {
    return <p className="text-sm text-muted-foreground">No failed calls in this range.</p>;
  }
  return (
    <div className="space-y-3" data-testid="audit-failures">
      <p className="text-sm">
        <span className="font-semibold tabular-nums">{total.toLocaleString()}</span> failed call
        {total === 1 ? "" : "s"} in this range.
      </p>
      <div className="grid gap-6 md:grid-cols-3">
        <FailureList title="Category" rows={byCategory} names={failureCategoryNames} />
        <FailureList title="Source" rows={bySource} names={sourceNames} />
        <FailureList title="Persona" rows={byPersona} names={{ "": "No persona" }} />
      </div>
    </div>
  );
}

function FailureList({ title, rows, names }: { title: string; rows: BreakdownEntry[]; names: Record<string, string> }) {
  return (
    <section>
      <h4 className="mb-2 text-xs font-medium text-muted-foreground">{title}</h4>
      <dl className="grid grid-cols-[1fr_auto] gap-x-4 gap-y-1.5 text-sm">
        {rows.map((d) => (
          <div key={d.dimension} className="contents">
            <dt title={d.dimension || undefined}>{names[d.dimension] ?? d.dimension}</dt>
            <dd className="text-right font-semibold tabular-nums">{d.count.toLocaleString()}</dd>
          </div>
        ))}
      </dl>
    </section>
  );
}
