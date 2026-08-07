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
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";

// EmbeddingStatusBadge surfaces the per-spec embedding state
// computed by the job queue. The badge variant and label
// communicate one of six states the operator can react to:
//
//   success: "N/M indexed"     — spec is fully indexed; semantic
//                                ranking is active.
//   info:    "indexing N/M"    — a worker is currently embedding
//                                this spec; counts move as the
//                                worker progresses.
//   warning: "queued"          — the job is in the queue waiting
//                                for a worker to pick it up (first
//                                attempt, no error history).
//   warning: "retrying (N tries)" — the job failed at least once
//                                and is queued for another try.
//                                Tooltip surfaces last_error so the
//                                operator can decide whether to
//                                wait, cancel, or investigate. Was
//                                the silent failure mode #479 fixed.
//   danger:  "failed"          — the job exhausted retries. The
//                                operator can click Retry next to
//                                the badge to force a fresh attempt.
//   muted:   "not indexed"     — the spec has no operations or
//                                no job has run for it (legacy
//                                state cleared by the next
//                                reconciler tick).
export function EmbeddingStatusBadge({ status }: { status?: APICatalogEmbeddingSpecStatus }) {
  if (!status) {
    return (
      <Badge variant="muted" title="Embedding status not yet loaded">
        loading…
      </Badge>
    );
  }
  const fully =
    status.operation_count > 0 && status.embedding_count === status.operation_count;
  const jobStatus = status.job_status ?? "";
  if (fully && (jobStatus === "succeeded" || jobStatus === "")) {
    return (
      <Badge
        variant="success"
        title={`${status.embedding_count}/${status.operation_count} operations indexed; semantic ranking active`}
      >
        {status.embedding_count}/{status.operation_count} indexed
      </Badge>
    );
  }
  if (jobStatus === "running") {
    // While the spec's UPSERT transaction is still pending, embedding_count
    // sits at 0 (or the previous run's value). The worker publishes
    // embedded_so_far at every chunk boundary so the badge ticks up
    // before the final commit. See #430.
    const progress = status.embedded_so_far ?? status.embedding_count;
    return (
      <Badge
        variant="info"
        title={`Worker is embedding this spec (attempt ${status.job_attempts ?? 1})`}
      >
        indexing {progress}/{status.operation_count}
      </Badge>
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
        <Badge
          variant="warning"
          title={`Retrying after failure. ${tries} so far. Last error: ${errMsg}`}
        >
          retrying ({tries})
        </Badge>
      );
    }
    return (
      <Badge variant="warning" title="Queued for embedding">
        queued
      </Badge>
    );
  }
  if (jobStatus === "failed") {
    return (
      <Badge variant="danger" title={status.job_last_error || "embedding failed"}>
        failed
      </Badge>
    );
  }
  if (status.operation_count === 0) {
    return (
      <Badge variant="muted" title="Spec has zero operations; nothing to embed">
        empty
      </Badge>
    );
  }
  return (
    <Badge
      variant="warning"
      title="No embedding job has run for this spec yet; reconciler will pick it up"
    >
      not indexed
    </Badge>
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
      // A polled summary, so role="status" (polite): Alert's default
      // role="alert" would interrupt a screen reader every time the worker
      // moves a count.
      <Alert variant="success" role="status">
        <AlertDescription>
          All {health.specs_total} specs indexed. Semantic ranking is active across this catalog.
        </AlertDescription>
      </Alert>
    );
  }
  const parts: string[] = [];
  if (health.specs_running > 0) parts.push(`${health.specs_running} running`);
  if (health.specs_pending > 0) parts.push(`${health.specs_pending} queued`);
  if (health.specs_failed > 0) parts.push(`${health.specs_failed} failed`);
  return (
    <Alert variant={health.specs_failed > 0 ? "destructive" : "warning"} role="status">
      <AlertDescription>
        {health.specs_indexed}/{health.specs_total} specs indexed
        {parts.length > 0 ? ` (${parts.join(", ")})` : ""}
      </AlertDescription>
    </Alert>
  );
}

// SourceBadge names where a component spec's content came from. The four
// source kinds are categories, not states, so each rides a distinct badge
// variant: operator-pasted (muted), uploaded (info), fetched from a URL
// (success), and platform-embedded (outline — the read-only kind that is
// re-seeded from its toolkit at startup).
const SOURCE_BADGES: Record<
  string,
  {
    icon: typeof FileText;
    label: string;
    variant: "muted" | "info" | "success" | "outline";
  }
> = {
  inline: { icon: FileText, label: "inline", variant: "muted" },
  upload: { icon: Upload, label: "upload", variant: "info" },
  url: { icon: LinkIcon, label: "URL", variant: "success" },
  embedded: { icon: Package, label: "embedded", variant: "outline" },
};

export function SourceBadge({ kind, url }: { kind: APICatalogSpec["source_kind"]; url?: string }) {
  // Fall back to the raw kind for any value the backend adds later, so an
  // unknown source_kind degrades to a plain badge instead of crashing the page.
  const config =
    SOURCE_BADGES[kind] ?? { icon: FileText, label: kind || "unknown", variant: "muted" as const };
  const Icon = config.icon;
  return (
    <Badge variant={config.variant} title={url || undefined}>
      <Icon aria-hidden /> {config.label}
    </Badge>
  );
}
