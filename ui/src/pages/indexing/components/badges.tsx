import { AlertTriangle, CheckCircle2, Loader2 } from "lucide-react";

import { type IndexVerdict } from "@/api/admin/indexjobs";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";

export function ProviderBanner({
  status,
  kind,
  model,
  dimension,
}: {
  status: "ok" | "unconfigured";
  kind: string;
  model: string;
  dimension: number;
}) {
  // The summary behind this banner re-polls every few seconds, so it is a
  // status region: role="alert" would re-announce the provider state on every
  // refresh.
  if (status === "ok") {
    return (
      <Alert variant="success" role="status">
        <CheckCircle2 />
        <AlertTitle>Embedding provider active</AlertTitle>
        <AlertDescription>
          {kind || "provider"}
          {model ? ` · ${model}` : ""} · {dimension}-dim
        </AlertDescription>
      </Alert>
    );
  }
  return (
    <Alert variant="warning" role="status">
      <AlertTriangle />
      <AlertTitle>Embedding provider unconfigured</AlertTitle>
      <AlertDescription>
        Semantic and hybrid ranking fall back to lexical until a provider is wired. Indexing is
        paused.
      </AlertDescription>
    </Alert>
  );
}

// VERDICT_META maps each server-computed verdict to its label, badge variant,
// and icon so the lead health word is consistent everywhere it renders.
const VERDICT_META: Record<
  IndexVerdict,
  {
    label: string;
    variant: "success" | "info" | "danger";
    spin?: boolean;
    Icon: typeof CheckCircle2;
  }
> = {
  healthy: { label: "Up to date", variant: "success", Icon: CheckCircle2 },
  indexing: { label: "Indexing…", variant: "info", spin: true, Icon: Loader2 },
  degraded: { label: "Degraded", variant: "danger", Icon: AlertTriangle },
};

export function VerdictBadge({ verdict }: { verdict: IndexVerdict }) {
  const m = VERDICT_META[verdict] ?? VERDICT_META.healthy;
  return (
    <Badge variant={m.variant}>
      <m.Icon className={m.spin ? "animate-spin" : undefined} /> {m.label}
    </Badge>
  );
}

// JOB_STATUS_VARIANT tints a job-queue state with the same semantics the rest
// of the platform's status pills use: queued work is a warning, in-flight is
// informational, a finished run is success or danger.
const JOB_STATUS_VARIANT: Record<string, "warning" | "info" | "success" | "danger"> = {
  pending: "warning",
  running: "info",
  succeeded: "success",
  failed: "danger",
};

export function JobStatusChip({ status }: { status: string }) {
  return (
    <Badge variant={JOB_STATUS_VARIANT[status] ?? "muted"} className="rounded px-1.5">
      {status}
    </Badge>
  );
}
