import { useState } from "react";
import { AlertTriangle, Check, X } from "lucide-react";
import {
  useApproveScriptVersion,
  useRejectScriptVersion,
  useScriptVersionReview,
} from "@/api/admin/hooks";
import type {
  ScriptDryRunAccount,
  ScriptDryRunOutput,
  ScriptFinding,
  ScriptGrants,
  VersionReview,
} from "@/api/admin/types";
import { DrawerShell } from "@/components/patterns/DrawerShell";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Separator } from "@/components/ui/separator";
import { DiffView, SourceView } from "./DiffView";
import {
  AuthorityNote,
  ConnectionsEditor,
  DestinationsEditor,
  GrantAxis,
} from "./ScriptGrantEditor";
import {
  CAPABILITIES,
  EMPTY_GRANT,
  incompleteDestinations,
  proposedGrant,
  widensAuthority,
} from "./scriptGrants";

// ScriptReviewDrawer is the decision surface: one version, what its code
// changes, what authority approving it would bind, and the two decisions.
//
// Approving means "this version is what executes now" — not "this version
// ran". The gate is re-read at execution, so approving an earlier version is a
// legitimate rollback and the surface offers it wherever a version is listed.
export function ScriptReviewDrawer({
  scriptID,
  scriptName,
  version,
  onClose,
}: {
  scriptID: string;
  scriptName: string;
  version: number;
  onClose: () => void;
}) {
  const {
    data: review,
    isLoading,
    error,
  } = useScriptVersionReview(scriptID, version);
  const approve = useApproveScriptVersion();
  const reject = useRejectScriptVersion();
  // edited holds the reviewer's changes and nothing else. The grant shown is
  // otherwise derived from the review, so a background refetch cannot silently
  // discard a grant somebody was part-way through editing — which is exactly
  // the state where losing an edit matters most.
  const [edited, setEdited] = useState<ScriptGrants | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);

  const grant = edited ?? (review ? proposedGrant(review) : EMPTY_GRANT);
  const setGrant = (update: (g: ScriptGrants) => ScriptGrants) =>
    setEdited(update(grant));

  const approved = review?.approved;
  const widens = widensAuthority(approved?.grants, grant);
  const busy = approve.isPending || reject.isPending;

  // Both decisions report their failure in place rather than closing: a
  // reviewer who was refused needs to see why next to what they submitted.
  const outcome = {
    onError: (e: unknown) =>
      setActionError(e instanceof Error ? e.message : "The action failed"),
    onSuccess: onClose,
  };
  const handleApprove = () => {
    setActionError(null);
    approve.mutate(
      {
        scriptID,
        version,
        grant: {
          connections: grant.connections,
          capabilities: grant.capabilities,
          destinations: grant.destinations,
        },
      },
      outcome,
    );
  };
  const handleReject = () => {
    setActionError(null);
    reject.mutate({ scriptID, version }, outcome);
  };

  return (
    <DrawerShell
      title={`${scriptName} v${version}`}
      onClose={onClose}
      busy={busy}
      className="max-w-3xl"
      footer={
        <ReviewActions
          review={review}
          busy={busy}
          error={actionError}
          incomplete={incompleteDestinations(grant)}
          onApprove={handleApprove}
          onReject={handleReject}
        />
      }
    >
      {isLoading && (
        <p className="text-sm text-muted-foreground">Loading this version...</p>
      )}
      {error && (
        <Alert variant="destructive">
          <AlertTriangle />
          <AlertDescription>
            This version could not be loaded, so there is nothing here to
            approve. The server may be unavailable.
          </AlertDescription>
        </Alert>
      )}
      {review && (
        <ReviewBody
          review={review}
          grant={grant}
          widens={widens}
          onGrantChange={setGrant}
        />
      )}
    </DrawerShell>
  );
}

// ReviewBody is everything a reviewer reads before deciding: what is being
// decided, the authority it binds, what validation found, the grant, and the
// code change.
function ReviewBody({
  review,
  grant,
  widens,
  onGrantChange,
}: {
  review: VersionReview;
  grant: ScriptGrants;
  widens: boolean;
  onGrantChange: (update: (g: ScriptGrants) => ScriptGrants) => void;
}) {
  const approved = review.approved;
  return (
    <>
      <ReviewHeader review={review} />
      <AuthorityNote grants={grant} author={review.version.author} />
      <DryRunAccount review={review} />
      <Findings findings={review.findings ?? []} />
      <Separator />
      <GrantSection
        review={review}
        grant={grant}
        widens={widens}
        onGrantChange={onGrantChange}
      />
      <Separator />
      <section className="space-y-2">
        <h4 className="text-sm font-medium">
          {approved ? `Code changes since v${approved.version}` : "Code"}
        </h4>
        <ReviewCode review={review} />
      </section>
    </>
  );
}

// GrantSection is the authority half of the decision: the three axes a grant
// is made of, each read against what the script holds today.
function GrantSection({
  review,
  grant,
  widens,
  onGrantChange,
}: {
  review: VersionReview;
  grant: ScriptGrants;
  widens: boolean;
  onGrantChange: (update: (g: ScriptGrants) => ScriptGrants) => void;
}) {
  const approved = review.approved;
  return (
    <section className="space-y-3">
      <h4 className="flex items-center gap-2 text-sm font-medium">
        Capabilities
        {widens && <Badge variant="warning">Widens authority</Badge>}
      </h4>
      <GrantAxis
        label="Host functions"
        help="What this script may call. A call it was not granted is refused at run time."
        options={CAPABILITIES}
        granted={grant.capabilities}
        previous={approved?.grants.capabilities ?? []}
        hasBaseline={!!approved}
        onChange={(capabilities) =>
          onGrantChange((g) => ({ ...g, capabilities }))
        }
      />
      <ConnectionsEditor
        granted={grant.connections}
        previous={approved?.grants.connections ?? []}
        referenced={review.referenced.connections}
        hasBaseline={!!approved}
        onChange={(connections) =>
          onGrantChange((g) => ({ ...g, connections }))
        }
      />
      {review.referenced.dynamic_connections && (
        <Alert variant="destructive">
          <AlertTriangle />
          <AlertDescription>
            This version computes at least one connection name instead of naming
            it, so the list above is not the whole list. What it queries cannot
            be checked by reading the code.
          </AlertDescription>
        </Alert>
      )}
      <DestinationsEditor
        granted={grant.destinations}
        previous={approved?.grants.destinations ?? []}
        hasBaseline={!!approved}
        onChange={(destinations) =>
          onGrantChange((g) => ({ ...g, destinations }))
        }
      />
      {review.referenced.dynamic_destinations && (
        <Alert variant="destructive">
          <AlertTriangle />
          <AlertDescription>
            This version computes at least one destination name instead of naming
            it, so the list above is not the whole list. Where it sends data
            cannot be checked by reading the code.
          </AlertDescription>
        </Alert>
      )}
    </section>
  );
}

// ReviewHeader states what is being decided: who wrote it, when, and whether
// anything of this script is running today.
function ReviewHeader({ review }: { review: VersionReview }) {
  const { version, approved } = review;
  return (
    <div className="space-y-1">
      <div className="flex flex-wrap items-center gap-2 text-sm">
        <Badge variant="outline" className="font-mono">
          v{version.version}
        </Badge>
        <Badge variant={version.status === "draft" ? "warning" : "muted"}>
          {version.status}
        </Badge>
        {approved ? (
          <Badge variant="info">Running v{approved.version} today</Badge>
        ) : (
          <Badge variant="info">Nothing of this script runs yet</Badge>
        )}
      </div>
      <p className="text-xs text-muted-foreground">
        Written by {version.author || "unknown"} on{" "}
        {new Date(version.created_at).toLocaleString()}.
        {approved?.approved_by && (
          <>
            {" "}
            The running version was approved by {approved.approved_by}
            {approved.approved_at &&
              ` on ${new Date(approved.approved_at).toLocaleString()}`}
            .
          </>
        )}
      </p>
    </div>
  );
}

// DryRunAccount says whether anybody has executed this exact source, and what
// happened when they did (#1364).
//
// Its absence is the case worth showing loudest: approving is agreeing to run
// code unattended, and "nobody has run this" is the single most useful thing a
// reviewer can know before doing so. The account is matched by the source
// itself, so it describes the code in this drawer and no other version.
function DryRunAccount({ review }: { review: VersionReview }) {
  const account = review.dry_run;
  if (!account) {
    return (
      <Alert>
        <AlertTriangle />
        <AlertDescription>
          Nobody has dry-run this source. Approving it means this code first
          executes unattended, under the grant below.
        </AlertDescription>
      </Alert>
    );
  }
  return (
    <div className="space-y-1.5">
      <div className="flex flex-wrap items-center gap-2 text-sm">
        <h4 className="text-sm font-medium">Dry run</h4>
        <Badge variant={account.status === "failed" ? "danger" : "success"}>
          {account.status}
        </Badge>
      </div>
      <p className="text-xs text-muted-foreground">{dryRunLine(account)}</p>
      {account.error && (
        <pre className="overflow-x-auto whitespace-pre-wrap text-xs text-destructive">
          {account.error}
        </pre>
      )}
      <DryRunOutputs outputs={account.outputs ?? []} />
    </div>
  );
}

// dryRunLine states who ran it, when, and what it cost, in one sentence — with
// the property that made a dry run safe to offer at all stated in the middle of
// it rather than in a footnote.
function dryRunLine(account: ScriptDryRunAccount): string {
  const outputs = (account.outputs ?? []).length;
  const queries = account.metrics.queries;
  return (
    `Run by ${account.requested_by || "unknown"} on ` +
    `${new Date(account.created_at).toLocaleString()}, as themselves and persisting ` +
    `nothing: ${queries} quer${queries === 1 ? "y" : "ies"}, ` +
    `${account.metrics.duration_ms} ms, ${outputs} output${outputs === 1 ? "" : "s"}.`
  );
}

// DryRunOutputs is the shape of what that run would have written. Nothing was
// written, so each entry names a size and no location.
function DryRunOutputs({ outputs }: { outputs: ScriptDryRunOutput[] }) {
  if (outputs.length === 0) return null;
  return (
    <ul className="space-y-0.5 text-xs text-muted-foreground">
      {outputs.map((o) => (
        <li key={`${o.name}-${o.destination ?? ""}`}>
          <span className="font-mono">{o.name}</span>: {o.row_count} row
          {o.row_count === 1 ? "" : "s"} as {o.format} ({o.bytes} bytes) to{" "}
          {o.destination || "the portal"}.
        </li>
      ))}
    </ul>
  );
}

// Findings renders the validator's complaints. They are advice, not a gate:
// the server refuses an unbindable grant, not an ugly script.
function Findings({ findings }: { findings: ScriptFinding[] }) {
  if (findings.length === 0) return null;
  return (
    <div className="space-y-1.5">
      <h4 className="text-sm font-medium">What validation found</h4>
      <ul className="space-y-1">
        {findings.map((f, i) => (
          <li key={`${f.message}-${i}`} className="text-xs">
            <Badge variant={f.severity === "error" ? "danger" : "warning"}>
              {f.severity || "note"}
            </Badge>{" "}
            {f.line ? <span className="font-mono">line {f.line}: </span> : null}
            {f.message}
            {f.hint && <span className="text-muted-foreground"> {f.hint}</span>}
          </li>
        ))}
      </ul>
    </div>
  );
}

// ReviewCode shows the change against the running version, or the whole source
// when there is no running version to compare against.
function ReviewCode({ review }: { review: VersionReview }) {
  const diff = review.approved?.source_diff;
  if (review.approved && !diff) {
    return (
      <>
        <p className="text-xs text-muted-foreground">
          The code is identical to the version running today. What would change
          is the capability grant above.
        </p>
        <SourceView source={review.version.source} />
      </>
    );
  }
  return diff ? (
    <DiffView diff={diff} />
  ) : (
    <SourceView source={review.version.source} />
  );
}

// ReviewActions is the decision, with the error attributed to the attempt that
// produced it.
//
// Two version states can be opened from the history but decided on by nobody:
// a rejected version and a superseded one are both refused by the approval
// transaction, so offering an Approve that always fails would be an affordance
// for a decision the platform does not accept. Rejecting is likewise offered
// only for a pending draft.
function ReviewActions({
  review,
  busy,
  error,
  incomplete,
  onApprove,
  onReject,
}: {
  review: VersionReview | undefined;
  busy: boolean;
  error: string | null;
  // incomplete names the granted destinations that do not yet say where they
  // write. The server refuses them, so the refusal is stated here instead,
  // next to the fields that answer it.
  incomplete: string[];
  onApprove: () => void;
  onReject: () => void;
}) {
  const status = review?.version.status;
  const rejectable = status === "draft";
  const refusal = approvalRefusal(status) || incompleteRefusal(incomplete);
  return (
    <div className="space-y-2">
      {error && (
        <Alert variant="destructive">
          <AlertTriangle />
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}
      <div className="flex items-center gap-2">
        <Button onClick={onApprove} disabled={busy || !review || !!refusal}>
          <Check /> {busy ? "Working..." : "Approve and bind this grant"}
        </Button>
        {rejectable && (
          <Button variant="outline" onClick={onReject} disabled={busy}>
            <X /> Reject
          </Button>
        )}
      </div>
      <DecisionNote review={review} rejectable={rejectable} refusal={refusal} />
    </div>
  );
}

// DecisionNote explains an absent affordance: why this version cannot be
// approved, or why there is nothing here to reject.
function DecisionNote({
  review,
  rejectable,
  refusal,
}: {
  review: VersionReview | undefined;
  rejectable: boolean;
  refusal: string;
}) {
  // Both can be true at once — a first approval of a script whose destination
  // has no address yet is exactly that case — and each explains a different
  // missing button, so neither may hide the other.
  const nothingToReject = !rejectable && !!review;
  if (!refusal && !nothingToReject) return null;
  return (
    <div className="space-y-1 text-xs text-muted-foreground">
      {refusal && <p>{refusal}</p>}
      {nothingToReject && (
        <p>
          There is nothing to reject here: this version is what the script already serves,
          and declining it leaves it unapproved, which is what it is now.
        </p>
      )}
    </div>
  );
}

// incompleteRefusal names the destinations still missing an address, which is
// the one refusal a reviewer can clear without leaving the drawer.
function incompleteRefusal(incomplete: string[]): string {
  if (incomplete.length === 0) return "";
  return `Say where ${incomplete.join(", ")} writes — a connection and a bucket — before approving. A destination without an address is a place nobody agreed to.`;
}

// approvalRefusal names why a version cannot be approved, or "" when it can.
// The wording is the store's own reason (internal/platform/scriptstore/
// approve.go, approvable): a version taken out of consideration is not made
// executable by re-opening it from the history.
function approvalRefusal(status: string | undefined): string {
  switch (status) {
    case "rejected":
      return "This version was rejected and cannot be approved. Propose a new version instead.";
    case "superseded":
      return "This version was superseded when a later one was approved, and cannot be approved. Propose a new version instead.";
    default:
      return "";
  }
}
