import { useState } from "react";
import { Area, AreaChart, CartesianGrid, Line, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { useStorageHistory, useStorageReport } from "@/api/admin/hooks";
import type { StorageReport, StoreView } from "@/api/admin/types";
import { ChartSkeleton } from "@/components/charts/ChartSkeleton";
import { SectionCard } from "@/components/patterns/SectionCard";
import { relativeTime } from "@/components/provenance/parts";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { formatBytes } from "@/lib/format";

// StoragePage is Admin > Storage (#2148): every store the platform writes to,
// used and free, how fast it is growing and when it would fill; a store's
// history on its row; and the detail behind them: each bucket's usage by
// purpose, what the last reconcile found, the largest tables and indexes, and
// each replica's webhook spool with the volume it is on.

const HISTORY_SPANS = [7, 30, 90, 180] as const;

/** bytesOrDash is a byte count, or a dash where there is none. */
function bytesOrDash(n: number | null | undefined): string {
  return n == null ? "—" : formatBytes(n);
}

/** perDay is a growth rate as the table shows it. */
function perDay(n: number | null): string {
  if (n == null) return "—";
  const sign = n < 0 ? "-" : "+";
  return `${sign}${formatBytes(Math.abs(n))}/day`;
}

/** span is a stretch of history in hours, as the table names it. */
function span(hours: number): string {
  if (hours < 24) return `${parseFloat(hours.toFixed(1))} h`;
  return `${parseFloat((hours / 24).toFixed(1))} d`;
}

/**
 * Growth is one rate cell. A rate the history is too short for says how much
 * history there is and how much it needs, rather than a dash or a first-hour
 * burst read as a daily rate (#2201).
 */
function Growth({ rate, history, needs }: { rate: number | null; history: number; needs: number }) {
  if (rate != null) return <>{perDay(rate)}</>;
  if (history >= needs) return <>—</>;
  return (
    <span className="text-muted-foreground">
      Not enough history
      <div className="text-xs">
        {span(history)} of {span(needs)}
      </div>
    </span>
  );
}

/** FillsOn is when a store fills, or why there is no date. */
function FillsOn({ s, onNavigate }: { s: StoreView; onNavigate: (path: string) => void }) {
  if (s.exhaustion_at) return <>{new Date(s.exhaustion_at).toLocaleDateString()}</>;
  if (s.total_bytes == null) {
    return (
      <Button
        type="button"
        variant="link"
        className="h-auto p-0"
        onClick={(e) => {
          e.stopPropagation();
          onNavigate("/admin/settings/storage");
        }}
      >
        No capacity known
      </Button>
    );
  }
  if (s.rate_7d_bytes_per_day == null && s.rate_30d_bytes_per_day == null && s.history_hours < s.rate_7d_min_history_hours) {
    return <>Not enough history</>;
  }
  return <>Not growing</>;
}

/**
 * capacitySentence names where the report's capacities come from, only the
 * sources it carries: a database limit, a bucket budget, the SeaweedFS cluster
 * a master is configured for, a spool's volume. Empty when it carries none.
 */
function capacitySentence(r: StorageReport): string {
  const sources: string[] = [];
  if (r.database.limit_bytes != null) sources.push("the database limit");
  if (r.stores.some((s) => s.capacity_from === "budget")) sources.push("bucket budgets");
  if (r.seaweedfs) sources.push("the disks the SeaweedFS cluster reports");
  if (r.spools.some((s) => s.fs_total_bytes != null)) sources.push("each spool's volume");
  if (sources.length === 0) return "";
  const list = sources.length === 1 ? sources[0] : `${sources.slice(0, -1).join(", ")} and ${sources[sources.length - 1]}`;
  return ` Capacity is ${list}.`;
}

export function StoragePage({ onNavigate }: { onNavigate: (path: string) => void }) {
  const { data, isLoading, isError } = useStorageReport();
  const [open, setOpen] = useState("");
  if (isError) {
    return (
      <Alert variant="destructive" className="py-2">
        <AlertDescription>Storage could not be read. Nothing stops writing while this page cannot.</AlertDescription>
      </Alert>
    );
  }
  return (
    <div className="space-y-4">
      <div className="flex justify-end">
        <Button type="button" size="sm" variant="outline" onClick={() => onNavigate("/admin/settings/storage")}>
          Storage settings
        </Button>
      </div>
      <StoresSection
        data={data}
        isLoading={isLoading}
        open={open}
        onOpen={(s) => setOpen(open === s ? "" : s)}
        onNavigate={onNavigate}
      />
      {open && <HistorySection store={open} report={data} />}
      {data?.seaweedfs?.error && (
        <Alert variant="destructive" className="py-2">
          <AlertDescription>
            The SeaweedFS cluster at {data.seaweedfs.master_url} could not be read: {data.seaweedfs.error}
          </AlertDescription>
        </Alert>
      )}
      {data && (
        <>
          <BucketsSection report={data} />
          <SpoolsSection report={data} />
          <TablesSection report={data} />
        </>
      )}
    </div>
  );
}

function LevelBadge({ level }: { level?: string }) {
  if (!level) return null;
  return (
    <Badge variant={level === "critical" ? "destructive" : "secondary"}>
      {level === "critical" ? "Critical" : "Warning"}
    </Badge>
  );
}

function StoresSection({
  data,
  isLoading,
  open,
  onOpen,
  onNavigate,
}: {
  data: StorageReport | undefined;
  isLoading: boolean;
  open: string;
  onOpen: (store: string) => void;
  onNavigate: (path: string) => void;
}) {
  return (
    <SectionCard title="Stores">
      {isLoading || !data ? (
        <ChartSkeleton height={160} />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Store</TableHead>
              <TableHead className="text-right">Used</TableHead>
              <TableHead className="text-right">Free</TableHead>
              <TableHead className="text-right">Capacity</TableHead>
              <TableHead className="text-right">Used %</TableHead>
              <TableHead className="text-right">Growth 7d</TableHead>
              <TableHead className="text-right">Growth 30d</TableHead>
              <TableHead>Fills on</TableHead>
              <TableHead>Level</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {data.stores.map((s) => (
              <TableRow
                key={s.store}
                className="cursor-pointer"
                data-state={open === s.store ? "selected" : undefined}
                onClick={() => onOpen(s.store)}
              >
                <TableCell className="font-medium">{s.name}</TableCell>
                <TableCell className="text-right tabular-nums">{formatBytes(s.used_bytes)}</TableCell>
                <TableCell className="text-right tabular-nums">{bytesOrDash(s.free_bytes)}</TableCell>
                <TableCell className="text-right tabular-nums">
                  {bytesOrDash(s.total_bytes)}
                  {s.capacity_from && (
                    <div className="text-xs text-muted-foreground">
                      {s.capacity_from === "budget" ? "budget" : "held + cluster free"}
                    </div>
                  )}
                </TableCell>
                <TableCell className="text-right tabular-nums">
                  {s.percent_used == null ? "—" : `${s.percent_used.toFixed(1)}%`}
                </TableCell>
                <TableCell className="text-right tabular-nums">
                  <Growth rate={s.rate_7d_bytes_per_day} history={s.history_hours} needs={s.rate_7d_min_history_hours} />
                </TableCell>
                <TableCell className="text-right tabular-nums">
                  <Growth rate={s.rate_30d_bytes_per_day} history={s.history_hours} needs={s.rate_30d_min_history_hours} />
                </TableCell>
                <TableCell>
                  <FillsOn s={s} onNavigate={onNavigate} />
                  {s.rate_used && s.exhaustion_at && (
                    <span className="ml-1 text-xs text-muted-foreground">(at the {s.rate_used} rate)</span>
                  )}
                </TableCell>
                <TableCell>
                  <LevelBadge level={s.level} />
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
      <p className="mt-2 text-xs text-muted-foreground">
        A store's fill date uses the slower of its 7-day and 30-day growth.{data && capacitySentence(data)} Select a
        store for its history.
      </p>
    </SectionCard>
  );
}

function HistorySection({ store, report }: { store: string; report: StorageReport | undefined }) {
  const [days, setDays] = useState<number>(30);
  const { data, isLoading } = useStorageHistory(store, days);
  const name = report?.stores.find((s) => s.store === store)?.name ?? store;
  const points = (data?.points ?? []).map((p) => ({ ...p, t: Date.parse(p.at) }));
  return (
    <SectionCard
      title={`History: ${name}`}
      action={
        <div className="flex gap-1">
          {HISTORY_SPANS.map((d) => (
            <Button
              key={d}
              type="button"
              size="xs"
              variant={days === d ? "default" : "outline"}
              aria-pressed={days === d}
              onClick={() => setDays(d)}
            >
              {d} days
            </Button>
          ))}
        </div>
      }
    >
      {isLoading ? (
        <ChartSkeleton height={200} />
      ) : points.length === 0 ? (
        <p className="text-sm text-muted-foreground">No samples yet. A store is sampled every 15 minutes.</p>
      ) : (
        <ResponsiveContainer width="100%" height={220}>
          <AreaChart data={points} margin={{ top: 4, right: 8, left: 0, bottom: 0 }}>
            <CartesianGrid strokeDasharray="3 3" className="stroke-muted" />
            <XAxis
              dataKey="t"
              type="number"
              domain={["dataMin", "dataMax"]}
              tickFormatter={(v: number) => new Date(v).toLocaleDateString()}
              className="text-xs"
              tick={{ fill: "hsl(var(--muted-foreground))" }}
            />
            <YAxis
              tickFormatter={(v: number) => formatBytes(v)}
              className="text-xs"
              tick={{ fill: "hsl(var(--muted-foreground))" }}
              width={72}
            />
            <Tooltip
              labelFormatter={(v: number) => new Date(v).toLocaleString()}
              formatter={(v: number, n: string) => [formatBytes(v), n === "used_bytes" ? "Used" : "Capacity"]}
              contentStyle={{
                backgroundColor: "hsl(var(--card))",
                border: "1px solid hsl(var(--border))",
                borderRadius: "0.375rem",
                fontSize: "0.75rem",
              }}
            />
            <Area dataKey="used_bytes" stroke="hsl(221, 83%, 53%)" fill="hsl(221, 83%, 53%)" fillOpacity={0.2} isAnimationActive={false} />
            <Line dataKey="total_bytes" stroke="hsl(0, 72%, 51%)" strokeDasharray="4 3" dot={false} isAnimationActive={false} />
          </AreaChart>
        </ResponsiveContainer>
      )}
    </SectionCard>
  );
}

function BucketsSection({ report }: { report: StorageReport }) {
  const reconcile = new Map(report.reconciles.map((r) => [r.purpose, r]));
  return (
    <SectionCard title="Buckets">
      {report.buckets.length === 0 ? (
        <p className="text-sm text-muted-foreground">No bucket usage is recorded yet.</p>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Bucket</TableHead>
              <TableHead>Purpose</TableHead>
              <TableHead className="text-right">Size</TableHead>
              <TableHead className="text-right">Objects</TableHead>
              <TableHead className="text-right">Orphaned objects</TableHead>
              <TableHead className="text-right">Dangling references</TableHead>
              <TableHead>Last listed</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {report.buckets.map((b) => (
              <TableRow key={`${b.bucket}/${b.purpose}`}>
                <TableCell className="font-medium">{b.bucket}</TableCell>
                <TableCell>{b.purpose}</TableCell>
                <TableCell className="text-right tabular-nums">{formatBytes(b.bytes)}</TableCell>
                <TableCell className="text-right tabular-nums">{b.objects.toLocaleString()}</TableCell>
                <TableCell className="text-right tabular-nums">{reconcile.get(b.purpose)?.orphaned.toLocaleString() ?? "—"}</TableCell>
                <TableCell className="text-right tabular-nums">{reconcile.get(b.purpose)?.dangling.toLocaleString() ?? "—"}</TableCell>
                <TableCell>{b.listed_at ? relativeTime(b.listed_at) : "Never"}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
      {report.last_scan && (
        <p className="mt-2 text-xs text-muted-foreground">
          Last full listing {relativeTime(report.last_scan.finished_at)}: {report.last_scan.objects.toLocaleString()}{" "}
          objects in {report.last_scan.duration_seconds.toFixed(1)}s.
        </p>
      )}
    </SectionCard>
  );
}

function SpoolsSection({ report }: { report: StorageReport }) {
  if (report.spools.length === 0) return null;
  return (
    <SectionCard title="Webhook spools">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Replica</TableHead>
            <TableHead className="text-right">Spool</TableHead>
            <TableHead className="text-right">Spool limit</TableHead>
            <TableHead className="text-right">Volume</TableHead>
            <TableHead className="text-right">Volume free</TableHead>
            <TableHead>Reported</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {report.spools.map((s) => (
            <TableRow key={s.replica}>
              <TableCell className="font-medium">
                {s.replica}
                <div className="text-xs text-muted-foreground">{s.dir}</div>
              </TableCell>
              <TableCell className="text-right tabular-nums">{formatBytes(s.bytes)}</TableCell>
              <TableCell className="text-right tabular-nums">{formatBytes(s.max_bytes)}</TableCell>
              <TableCell className="text-right tabular-nums">{bytesOrDash(s.fs_total_bytes)}</TableCell>
              <TableCell className="text-right tabular-nums">{bytesOrDash(s.fs_free_bytes)}</TableCell>
              <TableCell>
                {s.reporting ? relativeTime(s.updated_at) : <Badge variant="destructive">Not reporting</Badge>}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </SectionCard>
  );
}

function TablesSection({ report }: { report: StorageReport }) {
  return (
    <SectionCard title="Tables" collapsible defaultOpen={false} summary={`Database ${formatBytes(report.database.bytes)}`}>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Table or index</TableHead>
            <TableHead className="text-right">Size</TableHead>
            <TableHead className="text-right">Rows</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {report.database.tables.map((t) => (
            <TableRow key={t.name}>
              <TableCell className="font-medium">
                {t.name}
                {t.index && <span className="ml-2 text-xs text-muted-foreground">index</span>}
              </TableCell>
              <TableCell className="text-right tabular-nums">{formatBytes(t.bytes)}</TableCell>
              <TableCell className="text-right tabular-nums">{t.index ? "—" : t.rows.toLocaleString()}</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </SectionCard>
  );
}
