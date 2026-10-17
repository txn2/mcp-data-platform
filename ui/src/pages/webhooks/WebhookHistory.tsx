import { useMemo } from "react";
import {
  Bar,
  BarChart,
  CartesianGrid,
  Legend,
  Line,
  LineChart,
  ResponsiveContainer,
  Scatter,
  ScatterChart,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import type { WebhookHistory as History, WebhookStatusOverview } from "@/api/admin/types";
import { ChartSkeleton } from "@/components/charts/ChartSkeleton";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { formatBytes } from "@/lib/format";
import {
  ackSeries,
  bucketLabel,
  compactionSeries,
  flowSeries,
  replicaNames,
  spoolSeries,
  statusSeries,
  type Row,
} from "./historySeries";

// WebhookHistory is the webhooks page's history over the chosen range (#2150):
// what senders were answered, whether each receiver writes what it receives,
// how deep its spool runs, how long acknowledgements take and where that time
// goes, how far compaction runs behind, how large a window gets against the
// memory the compactor has, and what each source holds in storage.

const PALETTE = [
  "hsl(221, 83%, 53%)",
  "hsl(142, 71%, 45%)",
  "hsl(25, 95%, 53%)",
  "hsl(280, 65%, 60%)",
  "hsl(0, 72%, 51%)",
  "hsl(48, 96%, 53%)",
  "hsl(190, 80%, 42%)",
  "hsl(330, 81%, 60%)",
];
const STATUS_COLORS: Record<string, string> = {
  "202": "hsl(142, 71%, 45%)",
  "401": "hsl(0, 72%, 51%)",
  "413": "hsl(25, 95%, 53%)",
  "429": "hsl(38, 92%, 50%)",
  "503": "hsl(330, 81%, 60%)",
  "No answer": "hsl(215, 16%, 57%)",
};
const HEIGHT = 200;

const tooltipStyle = {
  backgroundColor: "hsl(var(--card))",
  border: "1px solid hsl(var(--border))",
  borderRadius: "0.375rem",
  fontSize: "0.75rem",
};
const tick = { fill: "hsl(var(--muted-foreground))" };

function color(i: number): string {
  return PALETTE[i % PALETTE.length] ?? "hsl(215, 16%, 57%)";
}

export function WebhookHistory({
  data,
  source,
  isLoading,
}: {
  data: WebhookStatusOverview | undefined;
  source: string;
  isLoading: boolean;
}) {
  if (!data || isLoading) {
    return (
      <SectionCard title="History">
        <ChartSkeleton height={HEIGHT} />
      </SectionCard>
    );
  }
  return (
    <>
      <p className="text-xs text-muted-foreground" data-testid="not-counted-note">
        These counts are what the platform answered. An answer a sender got from something in front of it, such as
        a 502 or 504 from a load balancer while a replica restarts, never reached the platform and is not counted
        here.
      </p>
      <div className="grid gap-4 xl:grid-cols-2">
        <StatusChart data={data} source={source} />
        <FlowChart data={data} />
        <SpoolDepthChart data={data} />
        <AckChart data={data} source={source} />
        <CompactionChart data={data} source={source} />
        <WindowSizing sizing={data.history.sizing} />
      </div>
      <StorageTable storage={data.history.storage} source={source} />
    </>
  );
}

function Empty({ children }: { children: React.ReactNode }) {
  return (
    <div
      className="flex items-center justify-center rounded-md border border-dashed text-sm text-muted-foreground"
      style={{ height: HEIGHT }}
    >
      {children}
    </div>
  );
}

function TimeAxis({ step }: { step: number }) {
  return (
    <XAxis
      dataKey="at"
      tickFormatter={(v: string) => bucketLabel(v, step)}
      className="text-xs"
      tick={tick}
      minTickGap={40}
    />
  );
}

function StatusChart({ data, source }: { data: WebhookStatusOverview; source: string }) {
  const { rows, statuses } = useMemo(() => statusSeries(data, source), [data, source]);
  return (
    <SectionCard title="Responses by status">
      {statuses.length === 0 ? (
        <Empty>No requests in this range.</Empty>
      ) : (
        <ResponsiveContainer width="100%" height={HEIGHT}>
          <BarChart data={rows} margin={{ top: 4, right: 8, left: 0, bottom: 0 }}>
            {TimeAxis({ step: data.bucket_seconds })}
            <YAxis className="text-xs" tick={tick} width={44} allowDecimals={false} />
            <Tooltip contentStyle={tooltipStyle} labelFormatter={(v: string) => bucketLabel(v, data.bucket_seconds)} />
            <Legend wrapperStyle={{ fontSize: "0.75rem" }} />
            {statuses.map((s, i) => (
              <Bar key={s} dataKey={s} stackId="status" fill={STATUS_COLORS[s] ?? color(i)} isAnimationActive={false} />
            ))}
          </BarChart>
        </ResponsiveContainer>
      )}
    </SectionCard>
  );
}

/** SeriesLines draws one line per column, the second of each pair dashed. */
function SeriesLines({ rows, columns, step, unit }: { rows: Row[]; columns: string[]; step: number; unit?: string }) {
  return (
    <ResponsiveContainer width="100%" height={HEIGHT}>
      <LineChart data={rows} margin={{ top: 4, right: 8, left: 0, bottom: 0 }}>
        <CartesianGrid strokeDasharray="3 3" className="stroke-muted" />
        {TimeAxis({ step })}
        <YAxis className="text-xs" tick={tick} width={52} unit={unit} />
        <Tooltip contentStyle={tooltipStyle} labelFormatter={(v: string) => bucketLabel(v, step)} />
        <Legend wrapperStyle={{ fontSize: "0.75rem" }} />
        {columns.map((c, i) => (
          <Line
            key={c}
            dataKey={c}
            stroke={color(Math.floor(i / 2))}
            strokeDasharray={i % 2 === 1 ? "4 3" : undefined}
            dot={false}
            isAnimationActive={false}
          />
        ))}
      </LineChart>
    </ResponsiveContainer>
  );
}

function FlowChart({ data }: { data: WebhookStatusOverview }) {
  const rows = useMemo(() => flowSeries(data), [data]);
  const names = replicaNames(data.history);
  return (
    <SectionCard title="Received vs written">
      {names.length === 0 ? (
        <Empty>No receiver reported in this range.</Empty>
      ) : (
        <>
          <SeriesLines
            rows={rows}
            columns={names.flatMap((n) => [`${n} received`, `${n} written`])}
            step={data.bucket_seconds}
          />
          <p className="mt-2 text-xs text-muted-foreground">
            Events per bucket each receiver accepted (solid) and wrote to object storage (dashed). Written below
            received is a burst the spool is holding; above it, the spool catching up.
          </p>
        </>
      )}
    </SectionCard>
  );
}

function SpoolDepthChart({ data }: { data: WebhookStatusOverview }) {
  const rows = useMemo(() => spoolSeries(data), [data]);
  const names = replicaNames(data.history);
  return (
    <SectionCard title="Spool depth">
      {names.length === 0 ? (
        <Empty>No receiver reported in this range.</Empty>
      ) : (
        <SeriesLines
          rows={rows}
          columns={names.flatMap((n) => [`${n} events`, `${n} oldest`])}
          step={data.bucket_seconds}
        />
      )}
      <p className="mt-2 text-xs text-muted-foreground">
        Events waiting in each spool (solid) and the oldest one's age in seconds (dashed).
      </p>
    </SectionCard>
  );
}

function AckChart({ data, source }: { data: WebhookStatusOverview; source: string }) {
  const rows = useMemo(() => ackSeries(data, source), [data, source]);
  return (
    <SectionCard title="Acknowledgement">
      {rows.length === 0 ? (
        <Empty>No delivery was acknowledged in this range.</Empty>
      ) : (
        <>
          <ResponsiveContainer width="100%" height={HEIGHT}>
            <LineChart data={rows} margin={{ top: 4, right: 8, left: 0, bottom: 0 }}>
              <CartesianGrid strokeDasharray="3 3" className="stroke-muted" />
              {TimeAxis({ step: data.bucket_seconds })}
              <YAxis className="text-xs" tick={tick} width={56} unit="ms" />
              <Tooltip
                contentStyle={tooltipStyle}
                labelFormatter={(v: string) => bucketLabel(v, data.bucket_seconds)}
                formatter={(v: number, name: string) => [`${Math.round(v)} ms`, name]}
              />
              <Legend wrapperStyle={{ fontSize: "0.75rem" }} />
              <Line dataKey="p99" stroke={color(4)} dot={false} isAnimationActive={false} />
              <Line dataKey="p50" stroke={color(0)} dot={false} isAnimationActive={false} />
              <Line dataKey="put" stroke={color(2)} strokeDasharray="4 3" dot={false} isAnimationActive={false} />
              <Line dataKey="mark" stroke={color(3)} strokeDasharray="4 3" dot={false} isAnimationActive={false} />
              <Line dataKey="buffer" stroke={color(1)} strokeDasharray="4 3" dot={false} isAnimationActive={false} />
            </LineChart>
          </ResponsiveContainer>
          <p className="mt-2 text-xs text-muted-foreground">
            p50 and p99 of the time to answer 202, and the average time a delivery spent in each stage: buffer,
            mark, and put (with a spool, the sync to disk).
          </p>
        </>
      )}
    </SectionCard>
  );
}

function CompactionChart({ data, source }: { data: WebhookStatusOverview; source: string }) {
  const rows = useMemo(() => compactionSeries(data, source), [data, source]);
  const points = rows.map((r) => ({ ...r, t: Date.parse(r.at) }));
  return (
    <SectionCard title="Compaction">
      {points.length === 0 ? (
        <Empty>No window was compacted in this range.</Empty>
      ) : (
        <>
          <ResponsiveContainer width="100%" height={HEIGHT}>
            <ScatterChart margin={{ top: 4, right: 8, left: 0, bottom: 0 }}>
              <CartesianGrid strokeDasharray="3 3" className="stroke-muted" />
              <XAxis
                dataKey="t"
                type="number"
                domain={["dataMin", "dataMax"]}
                tickFormatter={(v: number) => bucketLabel(new Date(v).toISOString(), data.bucket_seconds)}
                className="text-xs"
                tick={tick}
              />
              <YAxis dataKey="lag" className="text-xs" tick={tick} width={52} unit="s" />
              <Tooltip
                contentStyle={tooltipStyle}
                labelFormatter={() => ""}
                formatter={(v: number, name: string) => [name === "t" ? new Date(v).toLocaleString() : `${v}`, name]}
              />
              <Legend wrapperStyle={{ fontSize: "0.75rem" }} />
              <Scatter name="lag (s)" data={points} dataKey="lag" fill={color(0)} isAnimationActive={false} />
              <Scatter name="duration (s)" data={points.map((p) => ({ ...p, lag: p.duration }))} dataKey="lag" fill={color(2)} isAnimationActive={false} />
            </ScatterChart>
          </ResponsiveContainer>
          <p className="mt-2 text-xs text-muted-foreground">
            Each window's compaction: how long after the window ended it finished (lag, including the grace
            period), and how long it took.
          </p>
        </>
      )}
    </SectionCard>
  );
}

function WindowSizing({ sizing }: { sizing: History["sizing"] }) {
  const largest = sizing.largest;
  return (
    <SectionCard title="Window size">
      {!largest ? (
        <Empty>No window was compacted in this range.</Empty>
      ) : (
        <div className="space-y-3 text-sm" data-testid="window-sizing">
          <p>
            The largest window compacted, <span className="font-medium">{largest.source}</span> at{" "}
            {new Date(largest.window_start).toLocaleString()}, held{" "}
            <span className="font-medium tabular-nums">{formatBytes(largest.decoded_bytes)}</span> in memory for{" "}
            {largest.events.toLocaleString()} events ({formatBytes(largest.input_bytes)} read).
          </p>
          {sizing.memory_limit_bytes > 0 ? (
            <SizingBar percent={largest.percent_of_limit} limit={sizing.memory_limit_bytes} warnAt={sizing.warn_at_percent} />
          ) : (
            <p className="text-muted-foreground">This replica has no memory limit to set it against.</p>
          )}
          {sizing.warning && (
            <Alert variant="destructive" className="py-2">
              <AlertDescription>
                The compactor holds a whole window in memory, and this one took {Math.round(largest.percent_of_limit)}%
                of the memory limit. Shorten the source's compaction window or raise the limit before a window outgrows
                it.
              </AlertDescription>
            </Alert>
          )}
        </div>
      )}
    </SectionCard>
  );
}

function SizingBar({ percent, limit, warnAt }: { percent: number; limit: number; warnAt: number }) {
  const shown = Math.min(100, percent);
  return (
    <div>
      <div className="h-2 w-full overflow-hidden rounded bg-muted" aria-label="Largest window against the memory limit">
        <div
          className={percent >= warnAt ? "h-full bg-destructive" : "h-full bg-primary"}
          style={{ width: `${shown}%` }}
        />
      </div>
      <p className="mt-1 text-xs text-muted-foreground">
        {percent.toFixed(1)}% of the {formatBytes(limit)} memory limit; warned at {warnAt}%.
      </p>
    </div>
  );
}

function StorageTable({ storage, source }: { storage: History["storage"]; source: string }) {
  const rows = storage.filter((s) => !source || s.source === source);
  return (
    <SectionCard title="Storage by source">
      {rows.length === 0 ? (
        <p className="text-sm text-muted-foreground">No source holds anything in storage.</p>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Source</TableHead>
              <TableHead className="text-right">Raw</TableHead>
              <TableHead className="text-right">Raw files</TableHead>
              <TableHead className="text-right">Compacted</TableHead>
              <TableHead className="text-right">Compacted windows</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((s) => (
              <TableRow key={s.source}>
                <TableCell className="font-medium">{s.source}</TableCell>
                <TableCell className="text-right tabular-nums">{formatBytes(s.raw_bytes)}</TableCell>
                <TableCell className="text-right tabular-nums">{s.raw_segments.toLocaleString()}</TableCell>
                <TableCell className="text-right tabular-nums">{formatBytes(s.compacted_bytes)}</TableCell>
                <TableCell className="text-right tabular-nums">{s.compacted_windows.toLocaleString()}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </SectionCard>
  );
}
