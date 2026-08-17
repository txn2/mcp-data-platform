import { useState } from "react";
import { StatCard } from "@/components/cards/StatCard";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import type { CallArtifact, CallRecord } from "@/api/admin/types";
import { formatDuration } from "@/lib/formatDuration";
import { formatUser } from "@/lib/formatUser";
import { callSummary, OUTCOME_DESCRIPTION, OutcomeBadge, SATISFIED_BY_LABEL } from "./outcome";

// CallDetailView is one recorded call as it reads once loaded: what it was
// for, what ran, what came of it, and what a reviewer can do about it.
//
// It is the body both call surfaces render. An operator reading someone else's
// record and a user reading their own are looking at the same thing; the
// differences are passed in (where a session opens, whether the reader may
// act). Forking it into two components is how the two views would start
// answering the same question differently.

export function CallDetailView({
  record,
  sessionPath,
  assetPath,
  onNavigate,
  onPromote,
  onReject,
  isActing = false,
  actionError,
  showUser = true,
}: {
  record: CallRecord;
  /** Where this record's session opens, or undefined where it has no page. */
  sessionPath?: (sessionId: string) => string;
  /** Where an asset built from this record opens. */
  assetPath?: (assetId: string) => string;
  onNavigate?: (path: string) => void;
  /** Publishes the record. Omitted where the reader cannot act. */
  onPromote?: () => void;
  /** Declines the record, with a note. Omitted where the reader cannot act. */
  onReject?: (note: string) => void;
  isActing?: boolean;
  actionError?: string;
  /** Whether to name the caller. False on a reader's own record, where it
   * would repeat their own name the way the list's User column would. */
  showUser?: boolean;
}) {
  return (
    <div className="space-y-4">
      <CallStats record={record} />

      <SectionCard title={record.kind === "sql" ? "Statement" : "Request"}>
        <p className="pb-3 text-sm text-muted-foreground">
          {record.purpose || "The caller stated no purpose for this call."}
        </p>
        <pre className="overflow-x-auto rounded-md bg-muted/50 p-3 font-mono text-xs whitespace-pre-wrap">
          {callSummary(record)}
        </pre>
        {record.error_message && (
          <p className="pt-3 text-sm text-destructive">{record.error_message}</p>
        )}
      </SectionCard>

      <div className="grid gap-3 lg:grid-cols-2">
        <SectionCard title="What it addressed">
          <p className="pb-3 text-sm text-muted-foreground">
            The datasets a query read, or the endpoint an invocation called.
          </p>
          {record.targets.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              Nothing the platform could resolve to a catalog entity.
            </p>
          ) : (
            <ul className="space-y-1">
              {record.targets.map((target) => (
                <li key={target} className="font-mono text-xs break-all">
                  {target}
                </li>
              ))}
            </ul>
          )}
        </SectionCard>

        <SectionCard title="What came of it">
          <p className="pb-3 text-sm text-muted-foreground">
            {OUTCOME_DESCRIPTION[record.outcome]}
          </p>
          <CallArtifacts
            record={record}
            assetPath={assetPath}
            onNavigate={onNavigate}
          />
        </SectionCard>
      </div>

      <CallProvenance
        record={record}
        sessionPath={sessionPath}
        onNavigate={onNavigate}
        showUser={showUser}
      />

      {(onPromote || onReject) && (
        <CallReview
          record={record}
          onPromote={onPromote}
          onReject={onReject}
          isActing={isActing}
          actionError={actionError}
        />
      )}
    </div>
  );
}

/** The four numbers a reader scans first. */
function CallStats({ record }: { record: CallRecord }) {
  return (
    <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
      <StatCard label="Outcome" value={<OutcomeBadge outcome={record.outcome} />} />
      <StatCard
        label="Re-run by"
        value={`${record.reuse_count} session${record.reuse_count === 1 ? "" : "s"}`}
        detail="Later sessions that read this record and then ran what it holds."
      />
      <StatCard label="Duration" value={formatDuration(record.duration_ms)} />
      <StatCard label="Response" value={`${record.response_chars.toLocaleString()} chars`} />
    </div>
  );
}

/** What was built from the call, or why nothing was. */
function CallArtifacts({
  record,
  assetPath,
  onNavigate,
}: {
  record: CallRecord;
  assetPath?: (assetId: string) => string;
  onNavigate?: (path: string) => void;
}) {
  const artifacts = record.artifacts ?? [];
  if (artifacts.length === 0) {
    return (
      <p className="text-sm text-muted-foreground">
        Nothing cites this call yet.
      </p>
    );
  }
  return (
    <ul className="space-y-2">
      {artifacts.map((artifact) => (
        <li key={`${artifact.kind}:${artifact.id}`} className="text-sm">
          <ArtifactLink
            artifact={artifact}
            assetPath={assetPath}
            onNavigate={onNavigate}
          />
          <span className="ml-2 text-xs text-muted-foreground">
            {SATISFIED_BY_LABEL[artifact.kind]}
          </span>
        </li>
      ))}
    </ul>
  );
}

/** One artifact, as a link where the reader has a page to open. */
function ArtifactLink({
  artifact,
  assetPath,
  onNavigate,
}: {
  artifact: CallArtifact;
  assetPath?: (assetId: string) => string;
  onNavigate?: (path: string) => void;
}) {
  const openable = artifact.kind !== "capture" && assetPath && onNavigate;
  if (!openable) {
    return <span>{artifact.name}</span>;
  }
  return (
    <button
      type="button"
      className="text-left underline-offset-4 hover:underline"
      onClick={() => onNavigate(assetPath(artifact.id))}
    >
      {artifact.name}
    </button>
  );
}

/** Where the call came from: who made it, in which session, through what. */
function CallProvenance({
  record,
  sessionPath,
  onNavigate,
  showUser,
}: {
  record: CallRecord;
  sessionPath?: (sessionId: string) => string;
  onNavigate?: (path: string) => void;
  showUser: boolean;
}) {
  return (
    <SectionCard title="Where it came from">
      <dl className="grid gap-3 text-sm sm:grid-cols-2 lg:grid-cols-3">
        <Field label="Tool" value={record.tool_name} />
        <Field label="Connection" value={record.connection || "-"} />
        {showUser && (
          <Field label="Caller" value={formatUser(record.user_id ?? "", record.user_email)} />
        )}
        <Field label="Persona" value={record.persona || "-"} />
        <Field label="When" value={new Date(record.created_at).toLocaleString()} />
        <div>
          <dt className="text-xs text-muted-foreground">Session</dt>
          <dd className="font-mono text-xs">
            {record.session_id && sessionPath && onNavigate ? (
              <button
                type="button"
                className="underline-offset-4 hover:underline"
                onClick={() => onNavigate(sessionPath(record.session_id!))}
              >
                {record.session_id}
              </button>
            ) : (
              record.session_id || "-"
            )}
          </dd>
        </div>
        <div className="sm:col-span-2 lg:col-span-3">
          <dt className="text-xs text-muted-foreground">
            Reference an agent cites this call by
          </dt>
          <dd className="font-mono text-xs break-all">{record.reference}</dd>
        </div>
      </dl>
    </SectionCard>
  );
}

function Field({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="truncate" title={value}>
        {value}
      </dd>
    </div>
  );
}

/**
 * callDecision is what the record already says about itself: what it was
 * published as, why it was declined, or why it cannot be published yet. It
 * returns null when the record is still open, which is when the controls
 * belong on the page instead.
 *
 * A decided record shows the decision rather than the controls: what a promoted
 * record became is the thing worth reading, and re-promoting it would create a
 * second catalog entry for the same query.
 */
function callDecision(record: CallRecord) {
  if (record.promoted_urn) {
    return (
      <SectionCard title="Published">
        <p className="text-sm">
          Promoted by {record.promoted_by || "an operator"} as{" "}
          <span className="font-mono text-xs break-all">{record.promoted_urn}</span>
        </p>
      </SectionCard>
    );
  }
  if (record.rejected_at) {
    return (
      <SectionCard title="Declined">
        <p className="text-sm">
          Declined by {record.rejected_by || "an operator"}
          {record.rejection_note ? `: ${record.rejection_note}` : "."}
        </p>
      </SectionCard>
    );
  }
  if (record.outcome !== "satisfied") {
    return (
      <SectionCard title="Not yet publishable">
        <p className="text-sm text-muted-foreground">
          Only a call something was built from can be published: an asset, an
          export, or a capture that named it. {OUTCOME_DESCRIPTION[record.outcome]}
        </p>
      </SectionCard>
    );
  }
  return null;
}

/**
 * The decision a reviewer makes about a record: publish it, or decline it with
 * a note so it is not offered again.
 */
function CallReview({
  record,
  onPromote,
  onReject,
  isActing,
  actionError,
}: {
  record: CallRecord;
  onPromote?: () => void;
  onReject?: (note: string) => void;
  isActing: boolean;
  actionError?: string;
}) {
  const [note, setNote] = useState("");

  const decided = callDecision(record);
  if (decided) return decided;

  return (
    <SectionCard title="Publish this call">
      <div className="space-y-3">
        <p className="text-sm text-muted-foreground">
          {record.kind === "sql"
            ? "A query becomes a Query entity in the data catalog, on every dataset it reads."
            : "An API call becomes a saved example on its endpoint, shown to whoever reads that endpoint's schema next."}
        </p>
        {record.satisfied_by && (
          <p className="text-sm text-muted-foreground">
            Satisfied because it was {SATISFIED_BY_LABEL[record.satisfied_by]}
            {record.reuse_count > 0
              ? `, and re-run by ${record.reuse_count} later session${record.reuse_count === 1 ? "" : "s"}.`
              : "."}
          </p>
        )}
        <Textarea
          aria-label="Why this call is not worth publishing"
          placeholder="Why not, if you are declining it"
          value={note}
          onChange={(e) => setNote(e.target.value)}
          rows={2}
        />
        {actionError && <p className="text-sm text-destructive">{actionError}</p>}
        <div className="flex gap-2">
          {onPromote && (
            <Button onClick={onPromote} disabled={isActing}>
              Publish
            </Button>
          )}
          {onReject && (
            <Button
              variant="outline"
              onClick={() => onReject(note)}
              disabled={isActing}
            >
              Decline
            </Button>
          )}
        </div>
      </div>
    </SectionCard>
  );
}
