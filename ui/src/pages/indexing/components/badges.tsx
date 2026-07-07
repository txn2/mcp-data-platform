import { AlertTriangle, CheckCircle2, Loader2 } from "lucide-react";
import { type IndexVerdict } from "@/api/admin/indexjobs";
import { STATUS_COLORS } from "./helpers";

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
  if (status === "ok") {
    return (
      <div className="flex flex-wrap items-center gap-x-4 gap-y-1 rounded-lg border border-emerald-500/30 bg-emerald-500/5 px-4 py-2 text-sm">
        <span className="flex items-center gap-1.5 font-medium text-emerald-600 dark:text-emerald-400">
          <CheckCircle2 className="h-4 w-4" /> Embedding provider active
        </span>
        <span className="text-muted-foreground">
          {kind || "provider"}
          {model ? ` · ${model}` : ""} · {dimension}-dim
        </span>
      </div>
    );
  }
  return (
    <div className="flex flex-wrap items-center gap-x-4 gap-y-1 rounded-lg border border-amber-500/40 bg-amber-500/10 px-4 py-2 text-sm">
      <span className="flex items-center gap-1.5 font-medium text-amber-700 dark:text-amber-400">
        <AlertTriangle className="h-4 w-4" /> Embedding provider unconfigured
      </span>
      <span className="text-muted-foreground">
        Semantic and hybrid ranking fall back to lexical until a provider is wired. Indexing is paused.
      </span>
    </div>
  );
}

// VERDICT_META maps each server-computed verdict to its label, palette,
// and icon so the lead health word is consistent everywhere it renders.
const VERDICT_META: Record<
  IndexVerdict,
  { label: string; text: string; bg: string; border: string; spin?: boolean; Icon: typeof CheckCircle2 }
> = {
  healthy: {
    label: "Up to date",
    text: "text-emerald-600 dark:text-emerald-400",
    bg: "bg-emerald-500/10",
    border: "border-emerald-500/30",
    Icon: CheckCircle2,
  },
  indexing: {
    label: "Indexing…",
    text: "text-blue-600 dark:text-blue-400",
    bg: "bg-blue-500/10",
    border: "border-blue-500/30",
    spin: true,
    Icon: Loader2,
  },
  degraded: {
    label: "Degraded",
    text: "text-red-600 dark:text-red-400",
    bg: "bg-red-500/10",
    border: "border-red-500/40",
    Icon: AlertTriangle,
  },
};

export function VerdictBadge({ verdict }: { verdict: IndexVerdict }) {
  const m = VERDICT_META[verdict] ?? VERDICT_META.healthy;
  return (
    <span
      className={`inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-[11px] font-medium ${m.border} ${m.bg} ${m.text}`}
    >
      <m.Icon className={`h-3 w-3 ${m.spin ? "animate-spin" : ""}`} /> {m.label}
    </span>
  );
}

export function JobStatusChip({ status }: { status: string }) {
  return (
    <span
      className="inline-flex items-center rounded px-1.5 py-0.5 text-[11px] font-medium"
      style={{
        color: STATUS_COLORS[status] ?? "hsl(var(--muted-foreground))",
        backgroundColor: `${STATUS_COLORS[status] ?? "hsl(var(--muted))"}1a`,
      }}
    >
      {status}
    </span>
  );
}
