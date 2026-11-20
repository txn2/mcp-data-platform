import { ChevronLeft } from "lucide-react";
import { RECENT_DECISION_DAYS, useRecentDecisions, type ApprovalRequest, type ApprovalStatus } from "@/api/portal/hooks/approvals";
import { Badge } from "@/components/ui/badge";
import { formatWhen } from "@/pages/scripts/runFormat";

// The Approvals page's views of one request and of the recent decisions
// (#2725): the way back to the list, how a decided request reads on its own
// page, and the Pending list's empty state with the requests decided recently.

export const STATUS_VARIANT: Record<ApprovalStatus, "warning" | "success" | "danger" | "muted"> = {
  pending: "warning",
  approved: "success",
  denied: "danger",
  withdrawn: "muted",
};

/** AllRequestsLink goes back to the list from one request. */
export function AllRequestsLink({ onNavigate }: { onNavigate: (path: string) => void }) {
  return (
    <button type="button" className="inline-flex items-center gap-1 text-sm text-primary hover:underline" onClick={() => onNavigate("/approvals")}>
      <ChevronLeft className="h-4 w-4" aria-hidden />
      All requests
    </button>
  );
}

const DECIDED_WORD: Record<ApprovalStatus, string> = {
  pending: "Pending",
  approved: "Approved",
  denied: "Denied",
  withdrawn: "Withdrawn",
};

// DecidedNote is how a decided request reads on its own page: who decided it,
// when, and that nothing is left to do (#2725).
export function DecidedNote({ request: r }: { request: ApprovalRequest }) {
  const tone =
    r.status === "approved"
      ? "border-green-200 bg-green-50 text-green-900 dark:border-green-900 dark:bg-green-950 dark:text-green-100"
      : r.status === "denied"
        ? "border-red-200 bg-red-50 text-red-900 dark:border-red-900 dark:bg-red-950 dark:text-red-100"
        : "bg-muted";
  const by = r.decided_by ? ` by ${r.decided_by}` : "";
  const when = r.decided_at ? ` on ${formatWhen(r.decided_at)}` : "";
  return (
    <div className={`space-y-0.5 rounded-md border p-3 text-sm ${tone}`} data-testid="approval-decided">
      <p className="font-medium">
        {DECIDED_WORD[r.status]}
        {by}
        {when}.
      </p>
      {r.decision_comment && <p>“{r.decision_comment}”</p>}
      <p>Nothing is left to do.</p>
    </div>
  );
}

// NothingPending is the Pending list with nothing waiting (#2725): it says so
// and lists the requests decided recently, with who decided each, so a reader
// who came for one finds it.
export function NothingPending({ onNavigate }: { onNavigate: (path: string) => void }) {
  const { data } = useRecentDecisions(true);
  const recent = data ?? [];
  return (
    <div className="space-y-4" data-testid="approvals-nothing-pending">
      <div className="space-y-1 rounded-md border border-dashed p-4">
        <p className="font-medium">Nothing is waiting for a decision.</p>
        <p className="text-sm text-muted-foreground">
          {recent.length > 0
            ? `Requests decided in the last ${RECENT_DECISION_DAYS} days are below.`
            : `Nothing was decided in the last ${RECENT_DECISION_DAYS} days.`}
        </p>
      </div>
      {recent.length > 0 && (
        <section aria-labelledby="recent-decisions" className="space-y-2">
          <h2 id="recent-decisions" className="text-sm font-semibold">
            Decided in the last {RECENT_DECISION_DAYS} days
          </h2>
          <div className="max-w-full overflow-x-auto rounded-md border">
            <table className="w-full text-sm">
              <thead className="text-left text-xs text-muted-foreground">
                <tr>
                  <th className="px-3 py-2 font-medium">Request</th>
                  <th className="px-3 py-2 font-medium">Asked by</th>
                  <th className="px-3 py-2 font-medium">Decision</th>
                  <th className="px-3 py-2 font-medium">Decided by</th>
                  <th className="px-3 py-2 font-medium">When</th>
                </tr>
              </thead>
              <tbody>
                {recent.map((r) => (
                  <tr key={r.id} className="border-t" data-testid={`recent-${r.id}`}>
                    <td className="px-3 py-2">
                      <button type="button" className="text-left text-primary hover:underline" onClick={() => onNavigate(`/approvals?request=${encodeURIComponent(r.id)}`)}>
                        {requestTitle(r)}
                      </button>
                    </td>
                    <td className="px-3 py-2">{r.requester_email}</td>
                    <td className="px-3 py-2">
                      <Badge variant={STATUS_VARIANT[r.status]}>{r.status}</Badge>
                    </td>
                    <td className="px-3 py-2">
                      {r.decided_by}
                      {r.self_approved && (
                        <Badge variant="muted" className="ml-2">
                          self-approved
                        </Badge>
                      )}
                    </td>
                    <td className="px-3 py-2 text-muted-foreground">{formatWhen(r.decided_at)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>
      )}
    </div>
  );
}

const TITLES: Partial<Record<ApprovalRequest["subject_kind"], (r: ApprovalRequest) => string>> = {
  call: (r) => `${r.calls_asked ?? 1} ${r.operation || r.tool || "call"} call(s) on ${r.connection ?? ""}`,
  site_release: (r) => `${r.site_name ?? ""} release ${r.version} in ${r.target ?? ""}`,
  site_environment: (r) => `${r.site_name ?? ""} ${r.target ?? ""}`,
};

function scriptTitle(r: ApprovalRequest): string {
  return `${r.script_name || r.script_id} version ${r.version}`;
}

/** requestTitle names what a request asks for, in a few words, for a list row. */
export function requestTitle(r: ApprovalRequest): string {
  return (TITLES[r.subject_kind] ?? scriptTitle)(r).trim();
}
