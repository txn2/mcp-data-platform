import {
  FileText,
  Link as LinkIcon,
  Package,
  Upload,
} from "lucide-react";

import {
  type APICatalogSpec,
  type APICatalogEmbeddingSpecStatus,
} from "@/api/admin/hooks";
import { cn } from "@/lib/utils";

// EmbeddingStatusBadge surfaces the per-spec embedding state
// computed by the job queue. The badge color and label
// communicate one of six states the operator can react to:
//
//   green:  "N/M indexed"     — spec is fully indexed; semantic
//                                ranking is active.
//   blue:   "indexing N/M"    — a worker is currently embedding
//                                this spec; counts move as the
//                                worker progresses.
//   amber:  "queued"          — the job is in the queue waiting
//                                for a worker to pick it up (first
//                                attempt, no error history).
//   amber+: "retrying (N tries)" — the job failed at least once
//                                and is queued for another try.
//                                Tooltip surfaces last_error so the
//                                operator can decide whether to
//                                wait, cancel, or investigate. Was
//                                the silent failure mode #479 fixed.
//   red:    "failed (last_error)" — the job exhausted retries.
//                                The operator can click Retry
//                                next to the badge to force a
//                                fresh attempt.
//   gray:   "not indexed"     — the spec has no operations or
//                                no job has run for it (legacy
//                                state cleared by the next
//                                reconciler tick).
export function EmbeddingStatusBadge({ status }: { status?: APICatalogEmbeddingSpecStatus }) {
  if (!status) {
    return (
      <span
        title="Embedding status not yet loaded"
        className="inline-flex items-center gap-1 rounded bg-muted px-1.5 py-0.5 text-xs text-muted-foreground"
      >
        loading…
      </span>
    );
  }
  const fully =
    status.operation_count > 0 && status.embedding_count === status.operation_count;
  const jobStatus = status.job_status ?? "";
  if (fully && (jobStatus === "succeeded" || jobStatus === "")) {
    return (
      <span
        title={`${status.embedding_count}/${status.operation_count} operations indexed; semantic ranking active`}
        className="inline-flex items-center gap-1 rounded bg-emerald-100 px-1.5 py-0.5 text-xs text-emerald-900 dark:bg-emerald-950/30 dark:text-emerald-200"
      >
        {status.embedding_count}/{status.operation_count} indexed
      </span>
    );
  }
  if (jobStatus === "running") {
    // While the spec's UPSERT transaction is still pending, embedding_count
    // sits at 0 (or the previous run's value). The worker publishes
    // embedded_so_far at every chunk boundary so the badge ticks up
    // before the final commit. See #430.
    const progress = status.embedded_so_far ?? status.embedding_count;
    return (
      <span
        title={`Worker is embedding this spec (attempt ${status.job_attempts ?? 1})`}
        className="inline-flex items-center gap-1 rounded bg-sky-100 px-1.5 py-0.5 text-xs text-sky-900 dark:bg-sky-950/30 dark:text-sky-200"
      >
        indexing {progress}/{status.operation_count}
      </span>
    );
  }
  if (jobStatus === "pending") {
    // A pending row with attempts > 0 is a retry, not a first-time
    // queue. Distinguishing the two is what closes the #479 silent-
    // failure mode: a doom-looping job formerly rendered as
    // "queued" through every retry with no indication anything was
    // wrong. The retry badge surfaces the attempt count up front
    // and the last error in the tooltip so an operator can decide
    // whether to wait, cancel, or investigate.
    const attempts = status.job_attempts ?? 0;
    if (attempts > 0) {
      const errMsg = status.job_last_error || "no error message recorded";
      const tries = attempts === 1 ? "1 try" : `${attempts} tries`;
      return (
        <span
          title={`Retrying after failure. ${tries} so far. Last error: ${errMsg}`}
          className="inline-flex items-center gap-1 rounded bg-amber-200 px-1.5 py-0.5 text-xs text-amber-900 dark:bg-amber-900/40 dark:text-amber-100"
        >
          retrying ({tries})
        </span>
      );
    }
    return (
      <span
        title="Queued for embedding"
        className="inline-flex items-center gap-1 rounded bg-amber-100 px-1.5 py-0.5 text-xs text-amber-900 dark:bg-amber-950/30 dark:text-amber-200"
      >
        queued
      </span>
    );
  }
  if (jobStatus === "failed") {
    return (
      <span
        title={status.job_last_error || "embedding failed"}
        className="inline-flex items-center gap-1 rounded bg-destructive/15 px-1.5 py-0.5 text-xs text-destructive"
      >
        failed
      </span>
    );
  }
  if (status.operation_count === 0) {
    return (
      <span
        title="Spec has zero operations; nothing to embed"
        className="inline-flex items-center gap-1 rounded bg-muted px-1.5 py-0.5 text-xs text-muted-foreground"
      >
        empty
      </span>
    );
  }
  return (
    <span
      title="No embedding job has run for this spec yet; reconciler will pick it up"
      className="inline-flex items-center gap-1 rounded bg-amber-100 px-1.5 py-0.5 text-xs text-amber-900 dark:bg-amber-950/30 dark:text-amber-200"
    >
      not indexed
    </span>
  );
}

// CatalogEmbeddingHealthBanner is the one-line summary at the
// top of the spec list. Operators check it before considering
// the catalog production-ready ("All specs indexed" is the
// green-light signal; a non-zero pending/failed count means
// the worker is still catching up or attention is needed).
export function CatalogEmbeddingHealthBanner({
  health,
}: {
  health: { specs_total: number; specs_indexed: number; specs_pending: number; specs_running: number; specs_failed: number };
}) {
  if (health.specs_total === 0) {
    return null;
  }
  const allIndexed =
    health.specs_indexed === health.specs_total &&
    health.specs_pending === 0 &&
    health.specs_running === 0 &&
    health.specs_failed === 0;
  if (allIndexed) {
    return (
      <div className="rounded-md border border-emerald-500/30 bg-emerald-500/10 px-3 py-2 text-xs text-emerald-100">
        All {health.specs_total} specs indexed. Semantic ranking is active across this catalog.
      </div>
    );
  }
  const parts: string[] = [];
  if (health.specs_running > 0) parts.push(`${health.specs_running} running`);
  if (health.specs_pending > 0) parts.push(`${health.specs_pending} queued`);
  if (health.specs_failed > 0) parts.push(`${health.specs_failed} failed`);
  return (
    <div
      className={cn(
        "rounded-md border px-3 py-2 text-xs",
        health.specs_failed > 0
          ? "border-destructive/40 bg-destructive/10 text-destructive"
          : "border-amber-500/30 bg-amber-500/10 text-amber-100",
      )}
    >
      {health.specs_indexed}/{health.specs_total} specs indexed
      {parts.length > 0 ? ` (${parts.join(", ")})` : ""}
    </div>
  );
}

export function SourceBadge({ kind, url }: { kind: APICatalogSpec["source_kind"]; url?: string }) {
  const configs: Record<string, { icon: typeof FileText; label: string; tone: string }> = {
    inline: { icon: FileText, label: "inline", tone: "bg-muted text-muted-foreground" },
    upload: { icon: Upload, label: "upload", tone: "bg-blue-100 text-blue-900 dark:bg-blue-950/30 dark:text-blue-200" },
    url: { icon: LinkIcon, label: "URL", tone: "bg-green-100 text-green-900 dark:bg-green-950/30 dark:text-green-200" },
    embedded: { icon: Package, label: "embedded", tone: "bg-purple-100 text-purple-900 dark:bg-purple-950/30 dark:text-purple-200" },
  };
  // Fall back to the raw kind for any value the backend adds later, so an
  // unknown source_kind degrades to a plain badge instead of crashing the page.
  const config = configs[kind] ?? { icon: FileText, label: kind || "unknown", tone: "bg-muted text-muted-foreground" };
  const Icon = config.icon;
  return (
    <span
      title={url || undefined}
      className={cn("inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-xs", config.tone)}
    >
      <Icon className="h-3 w-3" /> {config.label}
    </span>
  );
}
