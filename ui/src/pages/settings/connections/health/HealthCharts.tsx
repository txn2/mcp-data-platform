import type { AppDBHealthPoint, AppDBHealthReport, AppDBHealthSettings, AppDBThresholds } from "@/api/admin/hooks";
import { cn } from "@/lib/utils";
import { bytes, bytesParts, firstThreshold, levelOf } from "./healthFormat";

// The Health card's numbers drawn (#2528): four stat tiles over the report
// and the settings, and three small charts over the last seven days of the
// samples the health loop kept.

const LEVEL_STROKE = { "": "stroke-primary", warning: "stroke-amber-500", critical: "stroke-red-500" } as const;
const LEVEL_FILL = { "": "bg-primary", warning: "bg-amber-500", critical: "bg-red-500" } as const;

/** Sparkline draws values as a line; one value or none draws nothing. */
export function Sparkline({
  values,
  label,
  level = "",
  className,
}: {
  values: number[];
  label: string;
  level?: "" | "warning" | "critical";
  className?: string;
}) {
  if (values.length < 2) return <div className={cn("h-8", className)} aria-hidden="true" />;
  const lo = Math.min(...values);
  const hi = Math.max(...values);
  const span = hi - lo || 1;
  const points = values
    .map((v, i) => `${(i / (values.length - 1)) * 100},${30 - ((v - lo) / span) * 28 - 1}`)
    .join(" ");
  return (
    <svg viewBox="0 0 100 30" preserveAspectRatio="none" role="img" aria-label={label} className={cn("h-8 w-full", className)}>
      <polyline points={points} fill="none" strokeWidth={1.5} vectorEffect="non-scaling-stroke" className={LEVEL_STROKE[level]} />
    </svg>
  );
}

/** Bar draws a share of a whole with a tick at the alert threshold. */
function Bar({ share, tick, level }: { share: number; tick: number; level: "" | "warning" | "critical" }) {
  return (
    <div className="relative h-1.5 w-full rounded-full bg-muted" aria-hidden="true">
      <div className={cn("h-full rounded-full", LEVEL_FILL[level])} style={{ width: `${Math.min(100, Math.max(0, share * 100))}%` }} />
      {tick > 0 && tick <= 1 && (
        <span className="absolute -top-0.5 h-2.5 w-0.5 rounded bg-foreground/60" style={{ left: `${tick * 100}%` }} />
      )}
    </div>
  );
}

function Tile({ label, value, unit, children, footer }: { label: string; value: string; unit?: string; children?: React.ReactNode; footer: string }) {
  return (
    <div className="flex min-w-0 flex-col gap-1.5 rounded-xl border bg-muted/20 p-4" data-testid={`health-tile-${label}`}>
      <span className="text-[13px] text-muted-foreground">{label}</span>
      <span className="text-2xl font-semibold tabular-nums">
        {value}
        {unit && <span className="ml-1 text-[15px] font-medium text-muted-foreground">{unit}</span>}
      </span>
      {children}
      <span className="text-xs text-muted-foreground">{footer}</span>
    </div>
  );
}

function alertLine(t: AppDBThresholds | undefined, format: (v: number) => string): string {
  const at = firstThreshold(t);
  return at > 0 ? `Alert at ${format(at)}` : "No alert set";
}

/** StatTiles are size, connections, transaction id age and the cache hit ratio. */
export function StatTiles({
  r,
  settings,
  points,
}: {
  r: AppDBHealthReport;
  settings: AppDBHealthSettings | undefined;
  points: AppDBHealthPoint[];
}) {
  const sizes = points.map((p) => p.sample.size_bytes);
  const growth = sizes.length > 1 ? sizes[sizes.length - 1]! - sizes[0]! : 0;
  const [size, unit] = bytesParts(r.server.size_bytes);
  const connShare = r.connections.max > 0 ? r.connections.total / r.connections.max : 0;
  const connT = settings?.connections_percent;
  const xid = r.server.transaction_id_age;
  const xidT = settings?.transaction_id_age;
  const xidScale = Math.max(firstThreshold(xidT) * 1.25, xid, 1);
  return (
    <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
      <Tile
        label="Size"
        value={size}
        unit={unit}
        footer={growth === 0 ? "No change in 7 days" : `${growth > 0 ? "+" : "-"}${bytes(Math.abs(growth))} in 7 days`}
      >
        <Sparkline values={sizes} label={`Size over the last 7 days, ${bytes(sizes[0] ?? 0)} to ${bytes(r.server.size_bytes)}`} />
      </Tile>
      <Tile label="Connections" value={String(r.connections.total)} unit={`of ${r.connections.max}`} footer={alertLine(connT, (v) => `${v}%`)}>
        <Bar share={connShare} tick={firstThreshold(connT) / 100} level={levelOf(connT, connShare * 100)} />
      </Tile>
      <Tile label="Transaction ID age" value={xid.toLocaleString()} footer={alertLine(xidT, (v) => v.toLocaleString())}>
        <Bar share={xid / xidScale} tick={firstThreshold(xidT) / xidScale} level={levelOf(xidT, xid)} />
      </Tile>
      <Tile label="Cache hit ratio" value={String(Math.round(r.server.cache_hit_ratio * 1000) / 10)} unit="%" footer="Reads served from memory">
        <div className="h-1.5" aria-hidden="true" />
      </Tile>
    </div>
  );
}

function Trend({
  title,
  values,
  format,
  threshold,
  judged = values,
}: {
  title: string;
  values: number[];
  format: (v: number) => string;
  threshold: AppDBThresholds | undefined;
  /** judged are the numbers held to threshold, when they are not values. */
  judged?: number[];
}) {
  const first = values[0] ?? 0;
  const last = values[values.length - 1] ?? 0;
  const crossed = judged.reduce<"" | "warning" | "critical">((worst, v) => {
    const l = levelOf(threshold, v);
    return l === "critical" || (l === "warning" && worst === "") ? l : worst;
  }, "");
  const direction = last > first ? "rose" : last < first ? "fell" : "held";
  return (
    <div className="flex min-w-0 flex-col gap-1 rounded-xl border p-3" data-testid={`health-trend-${title}`}>
      <div className="flex items-baseline justify-between gap-2">
        <span className="text-[13px] font-medium">{title}</span>
        <span className="text-xs text-muted-foreground tabular-nums">
          {format(first)} → {format(last)}
        </span>
      </div>
      <Sparkline
        values={values}
        level={crossed}
        label={`${title} ${direction} from ${format(first)} to ${format(last)} over the last 7 days`}
        className="h-10"
      />
    </div>
  );
}

/** LastSevenDays draws size, idle sessions and the most dead rows over the samples. */
export function LastSevenDays({ points, settings }: { points: AppDBHealthPoint[]; settings: AppDBHealthSettings | undefined }) {
  const minutes = Math.round((settings?.interval_seconds ?? 300) / 60);
  const every = minutes <= 1 ? "minute" : `${minutes} minutes`;
  return (
    <section className="space-y-2" aria-labelledby="health-history-h">
      <div className="flex items-baseline justify-between">
        <h3 id="health-history-h" className="text-sm font-semibold">
          Last 7 days
        </h3>
        <span className="text-xs text-muted-foreground">Sampled every {every}</span>
      </div>
      {points.length === 0 ? (
        <p className="text-sm text-muted-foreground">No samples yet. The first arrives within {every}.</p>
      ) : (
        <div className="grid gap-3 sm:grid-cols-3">
          <Trend title="Size" values={points.map((p) => p.sample.size_bytes)} format={bytes} threshold={undefined} />
          <Trend
            title="Idle in transaction"
            values={points.map((p) => p.sample.idle_in_transaction)}
            format={(v) => String(v)}
            threshold={settings?.idle_in_transaction_seconds}
            judged={points.map((p) => p.sample.longest_idle_seconds)}
          />
          <Trend
            title="Most dead rows in a table"
            values={points.map((p) => p.sample.max_dead_ratio * 100)}
            format={(v) => `${Math.round(v)}%`}
            threshold={settings?.dead_tuple_percent}
          />
        </div>
      )}
    </section>
  );
}
