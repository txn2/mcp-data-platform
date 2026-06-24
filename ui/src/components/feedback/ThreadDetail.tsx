import { useState } from "react";
import { ArrowLeft, Trash2, Quote, GitBranch, CheckCircle2, XCircle, Lightbulb } from "lucide-react";
import {
  useThread,
  useThreadEvents,
  useThreadChain,
  useAppendThreadEvent,
  useUpdateThread,
  useRespondValidation,
  useDeleteThread,
  useCaptureThreadInsight,
} from "@/api/portal/hooks";
import type { ThreadEvent, ThreadStatus } from "@/api/portal/types";
import { useAuthStore } from "@/stores/auth";
import {
  KIND_CHIP,
  KIND_LABEL,
  STATUS_CHIP,
  STATUS_LABEL,
  MODERATION_STATUSES,
  formatRelative,
} from "./meta";

interface Props {
  threadId: string;
  canModerate: boolean;
  onBack: () => void;
  onDeleted: () => void;
}

// eventSummary renders a one-line label for non-comment timeline events.
function eventSummary(e: ThreadEvent): string | null {
  switch (e.event_type) {
    case "status_change":
    case "resolution": {
      const next = (e.metadata?.["new_status"] as string) ?? "";
      return next ? `changed status to ${STATUS_LABEL[next as ThreadStatus] ?? next}` : "changed status";
    }
    case "rating":
      return e.rating != null ? `rated ${e.rating}/5` : "left a rating";
    case "approval":
      return "approved";
    case "rejection":
      return "rejected";
    case "validation_request":
      return "requested validation";
    case "validation_result":
      return "recorded a validation result";
    case "insight_linked":
      return "linked an insight";
    case "changeset_linked":
      return "linked a changeset";
    default:
      return null;
  }
}

// KnowledgeChainPanel surfaces the thread -> insight -> changeset chain (#602):
// once a thread is resolved by a captured insight, show the insight and any
// knowledge changesets that insight produced (the applied data-catalog edits).
function KnowledgeChainPanel({ threadId, insightId }: { threadId: string; insightId: string }) {
  const { data: chain, isLoading } = useThreadChain(threadId, true);
  return (
    <div className="border-b bg-muted/30 p-3">
      <div className="mb-1 flex items-center gap-1.5 text-xs font-medium text-muted-foreground">
        <GitBranch className="h-3.5 w-3.5" /> Knowledge chain
      </div>
      <p className="text-xs">
        Resolved by insight{" "}
        <code className="rounded bg-muted px-1 font-mono" title={insightId}>
          {insightId.length > 14 ? `${insightId.slice(0, 14)}…` : insightId}
        </code>
      </p>
      {isLoading && <p className="mt-1 text-xs text-muted-foreground">Loading applied changes…</p>}
      {chain && chain.changesets.length === 0 && (
        <p className="mt-1 text-xs text-muted-foreground">No catalog changes applied from this insight yet.</p>
      )}
      {chain && chain.changesets.length > 0 && (
        <ul className="mt-1.5 space-y-1">
          {chain.changesets.map((cs) => (
            <li key={cs.id} className="flex items-start gap-1.5 text-xs">
              <span className="shrink-0 rounded bg-primary/10 px-1 py-0.5 font-medium text-primary">
                {cs.change_type}
              </span>
              <span className="min-w-0 flex-1 truncate font-mono text-muted-foreground" title={cs.target_urn}>
                {cs.target_urn}
              </span>
              {cs.rolled_back && (
                <span className="shrink-0 rounded bg-amber-500/10 px-1 py-0.5 text-amber-700 dark:text-amber-300">
                  rolled back
                </span>
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

export function ThreadDetail({ threadId, canModerate, onBack, onDeleted }: Props) {
  const { data: thread } = useThread(threadId);
  const { data: events, isLoading } = useThreadEvents(threadId);
  const append = useAppendThreadEvent();
  const update = useUpdateThread();
  const del = useDeleteThread();
  const respondValidation = useRespondValidation();
  const capture = useCaptureThreadInsight();
  const me = useAuthStore((s) => s.user);
  const [reply, setReply] = useState("");
  const [disputeReason, setDisputeReason] = useState("");

  const isAuthor = !!me?.email && thread?.author_email === me.email;
  const mayModerate = canModerate || !!me?.is_admin || isAuthor;
  // Capturing feedback as an insight requires apply_knowledge access (or admin),
  // the same capability that reviews and applies it; mirrors the backend gate.
  const canApply = (me?.tools?.includes("apply_knowledge") ?? false) || !!me?.is_admin;

  const respond = (result: "validated" | "disputed") =>
    respondValidation.mutate({ threadId, result, reason: result === "disputed" ? disputeReason.trim() : undefined });

  const postReply = (e: React.FormEvent) => {
    e.preventDefault();
    if (!reply.trim()) return;
    append.mutate(
      { threadId, event_type: "comment", body: reply.trim() },
      { onSuccess: () => setReply("") },
    );
  };

  const changeStatus = (status: ThreadStatus) => update.mutate({ id: threadId, status });

  if (!thread) {
    return <div className="p-4 text-sm text-muted-foreground">Loading…</div>;
  }

  return (
    <div className="flex h-full flex-col">
      {/* Header */}
      <div className="flex items-center gap-2 border-b p-3">
        <button onClick={onBack} className="rounded p-1 hover:bg-accent" aria-label="Back to list">
          <ArrowLeft className="h-4 w-4" />
        </button>
        <span className={`rounded-full px-2 py-0.5 text-xs font-medium ${KIND_CHIP[thread.kind]}`}>
          {KIND_LABEL[thread.kind]}
        </span>
        <span className={`rounded-full px-2 py-0.5 text-xs font-medium ${STATUS_CHIP[thread.status]}`}>
          {STATUS_LABEL[thread.status]}
        </span>
        {mayModerate && (
          <button
            onClick={() => del.mutate(threadId, { onSuccess: onDeleted })}
            disabled={del.isPending}
            className="ml-auto rounded p-1 text-destructive hover:bg-destructive/10 disabled:opacity-50"
            aria-label="Delete thread"
            title="Delete"
          >
            <Trash2 className="h-3.5 w-3.5" />
          </button>
        )}
      </div>
      {del.isError && (
        <p className="border-b px-3 py-1.5 text-xs text-destructive">Failed to delete this thread.</p>
      )}

      {/* Title + meta */}
      <div className="border-b p-3">
        {thread.title && <h3 className="text-sm font-semibold">{thread.title}</h3>}
        <p className="text-xs text-muted-foreground">
          {thread.author_email} · {formatRelative(thread.created_at)}
          {thread.target_version ? ` · on v${thread.target_version}` : ""}
          {thread.requires_resolution ? " · needs resolution" : ""}
        </p>
        {thread.anchor?.type === "text_quote" && (
          <p className="mt-1 flex items-start gap-1 rounded border-l-2 border-primary/40 bg-muted/40 px-2 py-1 text-xs italic text-muted-foreground">
            <Quote className="mt-0.5 h-3 w-3 shrink-0" />
            <span className="min-w-0 truncate">{thread.anchor.exact}</span>
          </p>
        )}
      </div>

      {/* Capture as insight: a reviewer turns actionable feedback into a pending
          insight that enters the apply_knowledge review queue. Shown only for
          unlinked correction/suggestion threads to apply_knowledge holders. */}
      {!thread.insight_id &&
        canApply &&
        (thread.kind === "correction" || thread.kind === "suggestion") && (
          <div className="border-b bg-primary/5 p-3">
            <p className="mb-2 text-xs text-muted-foreground">
              Promote this feedback into the review queue. It becomes a pending
              insight an agent can apply as durable knowledge, and this thread
              resolves with a link to it.
            </p>
            <button
              type="button"
              onClick={() => capture.mutate({ threadId })}
              disabled={capture.isPending}
              className="inline-flex items-center gap-1.5 rounded-md bg-primary px-3 py-1.5 text-xs font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
            >
              <Lightbulb className="h-3.5 w-3.5" />
              {capture.isPending ? "Capturing…" : "Capture as insight"}
            </button>
            {capture.isError && (
              <p className="mt-1.5 text-xs text-destructive">
                Could not capture this as an insight.
              </p>
            )}
          </div>
        )}

      {/* Knowledge chain (shown once the thread is linked to a captured insight) */}
      {thread.insight_id && (
        <KnowledgeChainPanel threadId={threadId} insightId={thread.insight_id} />
      )}

      {/* Validation request: the feedback author confirms or disputes (#603) */}
      {thread.validation_state === "pending" && isAuthor && (
        <div className="border-b bg-amber-500/10 p-3">
          <p className="mb-2 text-xs font-medium text-amber-700 dark:text-amber-300">
            Your validation was requested: is this resolved correctly?
          </p>
          <textarea
            value={disputeReason}
            onChange={(e) => setDisputeReason(e.target.value)}
            rows={2}
            placeholder="Reason (required to dispute)…"
            className="w-full resize-y rounded-md border bg-background px-2 py-1.5 text-xs"
          />
          <div className="mt-2 flex gap-2">
            <button
              type="button"
              onClick={() => respond("validated")}
              disabled={respondValidation.isPending}
              className="inline-flex items-center gap-1 rounded-md bg-emerald-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-emerald-700 disabled:opacity-50"
            >
              <CheckCircle2 className="h-3.5 w-3.5" /> Validate
            </button>
            <button
              type="button"
              onClick={() => respond("disputed")}
              disabled={respondValidation.isPending || !disputeReason.trim()}
              className="inline-flex items-center gap-1 rounded-md border border-destructive/40 px-3 py-1.5 text-xs font-medium text-destructive hover:bg-destructive/10 disabled:opacity-50"
              title={!disputeReason.trim() ? "Add a reason to dispute" : "Dispute and re-open"}
            >
              <XCircle className="h-3.5 w-3.5" /> Dispute
            </button>
          </div>
          {respondValidation.isError && (
            <p className="mt-1 text-xs text-destructive">Failed to record your response.</p>
          )}
        </div>
      )}

      {/* Timeline */}
      <div className="flex-1 space-y-3 overflow-auto p-3">
        {isLoading && <p className="text-xs text-muted-foreground">Loading timeline…</p>}
        {(events ?? []).map((e) => {
          const summary = eventSummary(e);
          return (
            <div key={e.id} className="text-sm">
              <p className="text-xs text-muted-foreground">
                <span className="font-medium text-foreground">{e.author_email}</span>{" "}
                {summary ?? "commented"} · {formatRelative(e.created_at)}
              </p>
              {e.body && <p className="mt-0.5 whitespace-pre-wrap">{e.body}</p>}
            </div>
          );
        })}
      </div>

      {/* Moderation controls */}
      {mayModerate && (
        <div className="flex items-center gap-2 border-t p-3">
          <span className="text-xs font-medium text-muted-foreground">Set status</span>
          <select
            value={thread.status}
            onChange={(e) => changeStatus(e.target.value as ThreadStatus)}
            disabled={update.isPending}
            className="rounded-md border bg-background px-2 py-1 text-xs"
          >
            {MODERATION_STATUSES.map((s) => (
              <option key={s} value={s}>
                {STATUS_LABEL[s]}
              </option>
            ))}
          </select>
        </div>
      )}

      {/* Reply box */}
      <form onSubmit={postReply} className="border-t p-3">
        <textarea
          value={reply}
          onChange={(e) => setReply(e.target.value)}
          rows={2}
          placeholder="Reply…"
          className="w-full resize-y rounded-md border bg-background px-2 py-1.5 text-sm"
        />
        {append.isError && (
          <p className="mt-1 text-xs text-destructive">Failed to post reply.</p>
        )}
        <div className="mt-2 flex justify-end">
          <button
            type="submit"
            disabled={!reply.trim() || append.isPending}
            className="rounded-md bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
          >
            {append.isPending ? "Posting…" : "Reply"}
          </button>
        </div>
      </form>
    </div>
  );
}
