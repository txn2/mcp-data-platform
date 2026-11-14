import { useSite, useSiteCompare, type Site } from "@/api/portal/hooks/sites";
import { useState } from "react";
import {
  useApprovals,
  useDecideApproval,
  useRecentCalls,
  type ApprovalKind,
  type ApprovalRequest,
  type ApprovalStatus,
  type RecentCall,
} from "@/api/portal/hooks/approvals";
import { Input } from "@/components/ui/input";
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
// reads their own and the held calls whose policies name them, and decides
// those. A request names a version of a form to be served to people who are
// not signed in, a version of a script to run as a tool-call handler, a site
// release, or a call a policy held, with the platform's reading of it and the
// requester's recent calls of the same operation.

const FILTERS: { value: ApprovalStatus | ""; label: string }[] = [
  { value: "pending", label: "Pending" },
  { value: "approved", label: "Approved" },
  { value: "denied", label: "Denied" },
  { value: "", label: "All" },
];

const KINDS: { value: ApprovalKind | ""; label: string }[] = [
  { value: "", label: "All types" },
  { value: "call", label: "Calls" },
  { value: "handler_version", label: "Handler versions" },
  { value: "form", label: "Forms" },
  { value: "site", label: "Sites" },
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
  const [kind, setKind] = useState<ApprovalKind | "">("");
  const { data, isLoading, error } = useApprovals({ status, kind });
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
        <Label htmlFor="approval-kind" className="text-xs text-muted-foreground">
          Type
        </Label>
        <select
          id="approval-kind"
          className="h-8 rounded-md border bg-background px-2 text-sm"
          value={kind}
          onChange={(e) => setKind(e.target.value as ApprovalKind | "")}
        >
          {KINDS.map((k) => (
            <option key={k.label} value={k.value}>
              {k.label}
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
  const own = site.pages_addresses?.[env] ?? "";
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
  const me = useAuthStore((s) => s.user);
  const decides = r.status === "pending" && mayDecide(r, isAdmin, me?.email ?? "", me?.persona ?? "");
  return (
    <div className="space-y-2 rounded-md border p-3" data-testid={`approval-${r.id}`}>
      <RequestBody request={r} onNavigate={onNavigate} decides={decides} />
      {r.decided_by && (
        <p className="text-xs text-muted-foreground">
          {r.status} by {r.decided_by} {r.decided_at ? formatWhen(r.decided_at) : ""}
          {r.decision_comment ? `: ${r.decision_comment}` : ""}
        </p>
      )}
      {decides && <Decide request={r} />}
    </div>
  );
}

function RequestBody({ request: r, onNavigate, decides }: { request: ApprovalRequest; onNavigate: (path: string) => void; decides: boolean }) {
  if (r.subject_kind === "call") return <CallRequest request={r} decides={decides} />;
  if (r.subject_kind === "site_release" || r.subject_kind === "site_environment") return <SiteRequest request={r} />;
  if (r.purpose === "call_handler") return <HandlerVersionRequest request={r} onNavigate={onNavigate} />;
  return <ScriptRequest request={r} onNavigate={onNavigate} />;
}

// mayDecide mirrors the server's rule, to show the controls only to someone
// it would not refuse: an administrator decides any request; a held call's is
// also decided by a person or persona its policies named, never by its
// requester unless an administrator.
export function mayDecide(r: ApprovalRequest, isAdmin: boolean, email: string, persona: string): boolean {
  if (isAdmin) return true;
  if (r.subject_kind !== "call") return false;
  const mine = email.toLowerCase();
  if (mine && r.requester_email.toLowerCase() === mine) return false;
  return (r.approvers ?? []).some((a) => (mine && a.toLowerCase() === mine) || (persona !== "" && a === persona));
}

// whoDecides names who may decide a held call's request.
function whoDecides(r: ApprovalRequest): string {
  const named = (r.approvers ?? []).filter((a) => a !== "administrators");
  return named.length > 0 ? `${named.join(", ")}, or an administrator` : "an administrator";
}

const RECENT_OUTCOME: Record<RecentCall["outcome"], string> = {
  ran: "Ran",
  ran_on_allowance: "Ran on an allowance",
  failed: "Failed",
  held: "Held",
  refused: "Refused",
};

// CallRequest is a call a policy held (#2437): what the platform read of it,
// which policies held it and what they said, who decides, and the requester's
// recent calls of the operation.
function CallRequest({ request: r, decides }: { request: ApprovalRequest; decides: boolean }) {
  return (
    <>
      <div className="flex flex-wrap items-center gap-2">
        <Badge variant="info">Call</Badge>
        <span className="font-medium">
          {r.operation || r.tool} on {r.connection}
        </span>
        <StatusBadges request={r} />
      </div>
      <CallFacts request={r} />
      {r.rendering && (
        <div className="space-y-1">
          <p className="text-sm font-medium">The call, as the platform read it</p>
          <pre className="max-w-full overflow-x-auto whitespace-pre-wrap break-words rounded border p-2 font-mono text-xs">{r.rendering}</pre>
          <p className="text-xs text-muted-foreground">
            Frozen when it was filed, and cannot be changed. Approving lets {r.requester_email} make that many more {r.operation || r.tool} calls
            on {r.connection} until the time set; past it, or once they are used, the policy decides again.
          </p>
        </div>
      )}
      <Allowance request={r} />
      <RecentCalls request={r} enabled={decides || r.status === "pending"} />
    </>
  );
}

// CallFacts are who asked, for what, why, what held the call and who decides.
function CallFacts({ request: r }: { request: ApprovalRequest }) {
  const persona = r.requester_persona ? `, as ${r.requester_persona}` : "";
  const held = r.handlers ?? [];
  return (
    <dl className="grid grid-cols-1 gap-x-3 gap-y-1 text-sm sm:grid-cols-[150px_minmax(0,1fr)]">
      <dt className="text-muted-foreground">Asked by</dt>
      <dd className="min-w-0 break-words">
        {r.requester_email}
        {persona}, {formatWhen(r.created_at)}
      </dd>
      <dt className="text-muted-foreground">Asks for</dt>
      <dd>{plural(r.calls_asked ?? 1, "call")}</dd>
      {r.reason && (
        <>
          <dt className="text-muted-foreground">Reason</dt>
          <dd className="min-w-0 break-words">“{r.reason}”</dd>
        </>
      )}
      {held.length > 0 && (
        <>
          <dt className="text-muted-foreground">Held by</dt>
          <dd className="min-w-0 space-y-0.5 break-words">
            {held.map((h) => (
              <p key={h.id}>
                {h.script}
                {h.summary ? `: “${h.summary}”` : ""}
              </p>
            ))}
          </dd>
        </>
      )}
      <dt className="text-muted-foreground">Who decides</dt>
      <dd>{whoDecides(r)}</dd>
    </dl>
  );
}

// Allowance is what an approved call request grants and how much of it is used.
function Allowance({ request: r }: { request: ApprovalRequest }) {
  if (r.status !== "approved" || !r.calls_approved) return null;
  const used = r.calls_used ?? 0;
  const spent = used >= r.calls_approved;
  return (
    <p className="text-sm" data-testid="allowance">
      {spent
        ? `${used} of ${r.calls_approved} used. The next call is held again by its policy.`
        : `${plural(r.calls_approved, "call")}${r.until ? ` until ${formatWhen(r.until)}` : ""}. ${used} used. Make the call again to use it.`}
    </p>
  );
}

// RecentCalls is the requester's calls of the operation in the last day.
function RecentCalls({ request: r, enabled }: { request: ApprovalRequest; enabled: boolean }) {
  const { data } = useRecentCalls(r.id, enabled);
  if (!data || data.length === 0) return null;
  return (
    <div className="space-y-1" data-testid="recent-calls">
      <p className="text-sm font-medium">
        {r.requester_email}&apos;s calls on {r.operation || r.tool}, last 24 hours
      </p>
      <div className="max-w-full overflow-x-auto">
        <table className="w-full text-xs">
          <thead className="text-muted-foreground">
            <tr>
              <th className="py-1 pr-2 text-left font-normal">Time</th>
              <th className="py-1 pr-2 text-left font-normal">Items</th>
              <th className="py-1 text-left font-normal">Outcome</th>
            </tr>
          </thead>
          <tbody>
            {data.map((c, i) => (
              <tr key={`${c.at}-${i}`} className="border-t">
                <td className="py-1 pr-2">{formatWhen(c.at)}</td>
                <td className="py-1 pr-2">{c.items}</td>
                <td className="py-1">{RECENT_OUTCOME[c.outcome] ?? c.outcome}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

// HandlerVersionRequest is a version of a script waiting to run as a
// tool-call handler (#2436): administrators decide it, and an approved
// version runs with the roles of the administrator who approved it.
function HandlerVersionRequest({ request: r, onNavigate }: { request: ApprovalRequest; onNavigate: (path: string) => void }) {
  return (
    <>
      <div className="flex flex-wrap items-center gap-2">
        <Badge variant="info">Handler version</Badge>
        <button type="button" className="font-medium underline" onClick={() => onNavigate(`/automations/${r.script_id}`)}>
          {r.script_name || r.script_id}
        </button>
        <span className="text-sm text-muted-foreground">version {r.version}</span>
        <StatusBadges request={r} />
      </div>
      <p className="text-sm">
        Saved by {r.requester_email} {formatWhen(r.created_at)}. If approved, it runs as this script&apos;s policies, transforms
        and watches with your roles, in place of the version approved before.
      </p>
      <Changes request={r} />
    </>
  );
}

function plural(n: number, noun: string): string {
  return `${n} ${noun}${n === 1 ? "" : "s"}`;
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

// Decide approves or denies a request; approving a held call grants calls
// until a deadline, at most what was asked.
function Decide({ request: r }: { request: ApprovalRequest }) {
  const id = r.id;
  const decide = useDecideApproval();
  const [comment, setComment] = useState("");
  const isCall = r.subject_kind === "call";
  const [calls, setCalls] = useState(String(r.calls_asked ?? 1));
  const [until, setUntil] = useState("");
  const send = (decision: "approve" | "deny") =>
    decide.mutate({
      id,
      decision,
      comment: comment.trim(),
      ...(isCall && decision === "approve"
        ? { calls: Number(calls) || undefined, until: until ? new Date(until).toISOString() : undefined }
        : {}),
    });
  return (
    <div className="space-y-2 border-t pt-2">
      {isCall && (
        <div className="flex flex-wrap items-center gap-2 text-sm">
          <Label htmlFor={`calls-${id}`}>Approve</Label>
          <Input
            id={`calls-${id}`}
            type="number"
            min={1}
            max={r.calls_asked ?? 1}
            className="h-8 w-20"
            value={calls}
            onChange={(e) => setCalls(e.target.value)}
          />
          <Label htmlFor={`until-${id}`}>calls until</Label>
          <Input
            id={`until-${id}`}
            type="datetime-local"
            className="h-8 w-56"
            value={until}
            onChange={(e) => setUntil(e.target.value)}
          />
          <span className="text-xs text-muted-foreground">You may approve fewer than asked. With no time, 24 hours from now.</span>
        </div>
      )}
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
