import { useSite, useSiteCompare, type Site } from "@/api/portal/hooks/sites";
import { useState } from "react";
import {
  useApprovals,
  useDecideApproval,
  type ApprovalRequest,
  type ApprovalStatus,
} from "@/api/portal/hooks/approvals";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { useAuthStore } from "@/stores/auth";
import { PreviewButton } from "@/pages/scripts/endpoints/FormPublish";
import { formatWhen } from "@/pages/scripts/runFormat";

// ApprovalsPage is the Approvals queue (#2437, #2494). An administrator reads
// every request and decides the pending ones, their own included; anyone else
// reads their own. A request names a version of a form to be served to people
// who are not signed in, and what changed since the version approved before;
// the approver previews that version before deciding it.

const FILTERS: { value: ApprovalStatus | ""; label: string }[] = [
  { value: "pending", label: "Pending" },
  { value: "approved", label: "Approved" },
  { value: "denied", label: "Denied" },
  { value: "", label: "All" },
];

const STATUS_VARIANT: Record<ApprovalStatus, "warning" | "success" | "danger" | "muted"> = {
  pending: "warning",
  approved: "success",
  denied: "danger",
  withdrawn: "muted",
};

export function ApprovalsPage({ onNavigate }: { onNavigate: (path: string) => void }) {
  const isAdmin = useAuthStore((s) => s.isAdmin());
  const [status, setStatus] = useState<ApprovalStatus | "">("pending");
  const { data, isLoading, error } = useApprovals({ status });
  return (
    <div className="space-y-4">
      <div className="flex items-center gap-2">
        <Label htmlFor="approval-status" className="text-xs text-muted-foreground">
          Status
        </Label>
        <select
          id="approval-status"
          className="h-8 rounded-md border bg-background px-2 text-sm"
          value={status}
          onChange={(e) => setStatus(e.target.value as ApprovalStatus | "")}
        >
          {FILTERS.map((f) => (
            <option key={f.label} value={f.value}>
              {f.label}
            </option>
          ))}
        </select>
      </div>
      {isLoading && <p className="text-sm text-muted-foreground">Loading...</p>}
      {error && (
        <Alert variant="destructive">
          <AlertDescription>The requests could not be read.</AlertDescription>
        </Alert>
      )}
      {data && data.length === 0 && <p className="text-sm text-muted-foreground">No requests.</p>}
      <ul className="space-y-3">
        {(data ?? []).map((r) => (
          <li key={r.id}>
            <RequestCard request={r} isAdmin={isAdmin} onNavigate={onNavigate} />
          </li>
        ))}
      </ul>
    </div>
  );
}

/** StatusBadges are the request's status and whether its asker approved it. */
function StatusBadges({ request: r }: { request: ApprovalRequest }) {
  return (
    <>
      <Badge variant={STATUS_VARIANT[r.status]}>{r.status}</Badge>
      {r.self_approved && <Badge variant="muted">self-approved</Badge>}
    </>
  );
}

/** ScriptRequest is what a form's approval shows: the form, its changes and a preview. */
function ScriptRequest({ request: r, onNavigate }: { request: ApprovalRequest; onNavigate: (path: string) => void }) {
  return (
    <>
      <div className="flex flex-wrap items-center gap-2">
        <button type="button" className="font-medium underline" onClick={() => onNavigate(`/automations/${r.script_id}`)}>
          {r.script_name || r.script_id}
        </button>
        <span className="text-sm text-muted-foreground">version {r.version}</span>
        <StatusBadges request={r} />
      </div>
      <p className="text-sm">
        Serve this form to people who are not signed in. Asked by {r.requester_email} {formatWhen(r.created_at)}.
      </p>
      {r.reason && <p className="text-sm text-muted-foreground">“{r.reason}”</p>}
      <Changes request={r} />
      <div className="flex flex-wrap items-center gap-2">
        <PreviewButton scriptId={r.script_id} version={r.version} label={`Preview version ${r.version}`} />
      </div>
    </>
  );
}

/** SiteRequest is what a site release's approval shows. */
function SiteRequest({ request: r }: { request: ApprovalRequest }) {
  if (r.purpose === "site_public") return <SitePublicRequest request={r} />;
  return (
    <>
      <div className="flex flex-wrap items-center gap-2">
        <span className="font-medium">{r.site_name}</span>
        <span className="text-sm text-muted-foreground">release {r.version}</span>
        <StatusBadges request={r} />
      </div>
      <p className="text-sm">
        Serve this release in {r.target}. Asked by {r.requester_email} {formatWhen(r.created_at)}.
      </p>
      {r.reason && <p className="text-sm text-muted-foreground">“{r.reason}”</p>}
      {r.site_name && <SiteRequestChanges site={r.site_name} target={r.target ?? ""} release={r.version} />}
    </>
  );
}

// SitePublicRequest is a request to open a private site environment to anyone
// (#2604): it names every address that would stop asking for a sign-in.
function SitePublicRequest({ request: r }: { request: ApprovalRequest }) {
  const data = useSite(r.site_name ?? "").data;
  const target = r.target ?? "";
  const env = data?.environments.find((e) => e.name === target);
  const hosts = publicHosts(data, target);
  return (
    <>
      <div className="flex flex-wrap items-center gap-2">
        <span className="font-medium">{r.site_name}</span>
        <span className="text-sm text-muted-foreground">{target}</span>
        <Badge variant="info">Make public</Badge>
        <StatusBadges request={r} />
      </div>
      <p className="text-sm">
        Open {target} to anyone, without a sign-in. Asked by {r.requester_email} {formatWhen(r.created_at)}.
      </p>
      {r.reason && <p className="text-sm text-muted-foreground">“{r.reason}”</p>}
      {hosts.length > 0 && (
        <div className="space-y-0.5">
          <p className="text-xs font-medium text-muted-foreground">Becomes public</p>
          {hosts.map((h) => (
            <p key={h} className="break-all font-mono text-sm">
              {h.replace(/^https?:\/\//, "")}
            </p>
          ))}
        </div>
      )}
      {(env?.release ?? 0) > 0 && <p className="text-xs text-muted-foreground">{target} serves release {env?.release} today. Its shares stop mattering while it is public.</p>}
    </>
  );
}

/** Every address of a site environment: its pages address and its domains. */
function publicHosts(site: Site | undefined, env: string): string[] {
  if (!site) return [];
  const own = site.addresses?.[env] ?? "";
  return [own, ...site.domains.filter((d) => d.environment === env).map((d) => d.hostname)].filter(Boolean);
}

// SiteRequestChanges is what approving would change on the environment (#2559),
// read from the two releases' file digests.
function SiteRequestChanges({ site, target, release }: { site: string; target: string; release: number }) {
  const data = useSite(site).data;
  const from = data?.environments.find((e) => e.name === target)?.release ?? 0;
  const cmp = useSiteCompare(site, from, data ? release : 0).data;
  if (!cmp) return null;
  return (
    <div className="flex flex-wrap items-center gap-2">
      <Badge variant="info">{cmp.changed.length} files changed</Badge>
      <Badge variant="success">{cmp.added.length} added</Badge>
      <Badge variant="danger">{cmp.removed.length} removed</Badge>
      <Badge variant="muted">{cmp.redirects_added.length} redirects added</Badge>
    </div>
  );
}

function RequestCard({ request: r, isAdmin, onNavigate }: { request: ApprovalRequest; isAdmin: boolean; onNavigate: (path: string) => void }) {
  return (
    <div className="space-y-2 rounded-md border p-3" data-testid={`approval-${r.id}`}>
      {r.subject_kind === "site_release" ? <SiteRequest request={r} /> : <ScriptRequest request={r} onNavigate={onNavigate} />}
      {r.decided_by && (
        <p className="text-xs text-muted-foreground">
          {r.status} by {r.decided_by} {r.decided_at ? formatWhen(r.decided_at) : ""}
          {r.decision_comment ? `: ${r.decision_comment}` : ""}
        </p>
      )}
      {isAdmin && r.status === "pending" && <Decide id={r.id} />}
    </div>
  );
}

function Changes({ request: r }: { request: ApprovalRequest }) {
  const c = r.changes;
  if (!c.approved_version) {
    return <p className="text-xs text-muted-foreground">No version of this form was approved before.</p>;
  }
  return (
    <details className="text-xs">
      <summary className="cursor-pointer text-muted-foreground">
        Changed since version {c.approved_version} (approved): {c.added} lines added, {c.removed} removed
      </summary>
      {c.diff ? (
        <pre className="mt-2 max-h-80 overflow-auto rounded bg-muted p-2 font-mono text-xs">{c.diff}</pre>
      ) : (
        <p className="mt-1 text-muted-foreground">The approved version is no longer kept, so no comparison is shown.</p>
      )}
      {c.truncated && <p className="mt-1 text-muted-foreground">The comparison is cut short here.</p>}
    </details>
  );
}

function Decide({ id }: { id: string }) {
  const decide = useDecideApproval();
  const [comment, setComment] = useState("");
  const send = (decision: "approve" | "deny") => decide.mutate({ id, decision, comment: comment.trim() });
  return (
    <div className="space-y-2">
      <Label htmlFor={`comment-${id}`} className="text-xs text-muted-foreground">
        Comment for the requester
      </Label>
      <Textarea id={`comment-${id}`} rows={2} value={comment} onChange={(e) => setComment(e.target.value)} />
      <div className="flex gap-2">
        <Button size="sm" disabled={decide.isPending} onClick={() => send("approve")}>
          Approve
        </Button>
        <Button size="sm" variant="outline" disabled={decide.isPending} onClick={() => send("deny")}>
          Deny
        </Button>
      </div>
      {decide.error && (
        <Alert variant="destructive">
          <AlertDescription>{decide.error.message}</AlertDescription>
        </Alert>
      )}
    </div>
  );
}
