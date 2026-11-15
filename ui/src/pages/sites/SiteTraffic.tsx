import { useState } from "react";
import { Bar, BarChart, CartesianGrid, Legend, Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { useReplicaTraffic, useSiteTraffic, type Site, type TrafficRange, type TrafficTotals } from "@/api/portal/hooks/sites";
import { SectionCard } from "@/components/patterns/SectionCard";
import { SegmentedControl } from "@/components/patterns/SegmentedControl";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { orderedEnvironments } from "./siteFormat";

// A site's traffic and latency (#2647), recorded by the platform itself: every
// replica tallies the requests it answers and adds them once a minute into
// rows its replicas share, so the figures need no Prometheus and no grant
// beyond seeing the site. Nothing a visitor's request carried is kept.

const RANGES: { value: TrafficRange; label: string; text: string }[] = [
  { value: "1h", label: "Last hour", text: "1h" },
  { value: "24h", label: "Last 24 hours", text: "24h" },
  { value: "7d", label: "Last 7 days", text: "7d" },
  { value: "30d", label: "Last 30 days", text: "30d" },
];

const CLASS_COLORS = {
  "2xx": "hsl(142, 71%, 45%)",
  "3xx": "hsl(217, 91%, 60%)",
  "4xx": "hsl(38, 92%, 50%)",
  "5xx": "hsl(0, 72%, 51%)",
} as const;

const LATENCY_COLORS = { p50_ms: "hsl(217, 91%, 60%)", p95_ms: "hsl(38, 92%, 50%)", p99_ms: "hsl(0, 72%, 51%)" } as const;

/** A latency figure in a reader's words. */
export function ms(v: number): string {
  if (!v) return "–";
  return v >= 1000 ? `${(v / 1000).toFixed(1)} s` : `${v < 10 ? v.toFixed(1) : Math.round(v)} ms`;
}

/** A count in a reader's words. */
export function count(n: number): string {
  return new Intl.NumberFormat(undefined, { notation: n >= 10000 ? "compact" : "standard" }).format(n);
}

function tick(iso: string, range: TrafficRange): string {
  const d = new Date(iso);
  return range === "1h" || range === "24h" ? d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }) : d.toLocaleDateString([], { month: "short", day: "numeric" });
}

const tooltipStyle = { backgroundColor: "hsl(var(--card))", border: "1px solid hsl(var(--border))", borderRadius: "0.375rem", fontSize: "0.75rem" };

// SiteTrafficTab is the Traffic view of one site.
export function SiteTrafficTab({ site }: { site: Site }) {
  const [range, setRange] = useState<TrafficRange>("24h");
  const [env, setEnv] = useState("");
  const { data, isLoading, error } = useSiteTraffic(site.name, range, env);
  const envs = orderedEnvironments(site.environments).map((e) => e.name);
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <SegmentedControl label="Range" value={range} onChange={setRange} options={RANGES} />
        <select className="h-8 rounded-md border bg-background px-2 text-sm" aria-label="Environment" value={env} onChange={(e) => setEnv(e.target.value)}>
          <option value="">All environments</option>
          {envs.map((n) => (
            <option key={n} value={n}>
              {n}
            </option>
          ))}
        </select>
      </div>
      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error.message}</AlertDescription>
        </Alert>
      )}
      {data && !data.recording && (
        <Alert data-testid="traffic-off">
          <AlertDescription>Recording is off on this deployment, so no new traffic is counted. An administrator turns it on in Admin &gt; Settings &gt; Sites.</AlertDescription>
        </Alert>
      )}
      {isLoading && <p className="text-sm text-muted-foreground">Loading…</p>}
      {data && <TrafficBody data={data} range={range} envs={envs} />}
    </div>
  );
}

function TrafficBody({ data, range, envs }: { data: NonNullable<ReturnType<typeof useSiteTraffic>["data"]>; range: TrafficRange; envs: string[] }) {
  if (data.total.requests === 0) {
    return (
      <div className="rounded border border-dashed p-6 text-center" data-testid="traffic-empty">
        <p className="font-medium">No requests in this range</p>
        <p className="text-xs text-muted-foreground">Requests show here within a minute of being answered.</p>
      </div>
    );
  }
  const series = data.series.map((p) => ({ at: p.at, ...p.classes, p50_ms: p.p50_ms, p95_ms: p.p95_ms, p99_ms: p.p99_ms }));
  return (
    <>
      <Figures totals={data.total} />
      <div className="grid gap-4 lg:grid-cols-2">
        <SectionCard title="Requests">
          <ResponsiveContainer width="100%" height={220}>
            <BarChart data={series} margin={{ top: 12, right: 8, left: 0, bottom: 0 }}>
              <CartesianGrid strokeDasharray="3 3" className="stroke-border" vertical={false} />
              <XAxis dataKey="at" tickFormatter={(v: string) => tick(v, range)} className="text-xs" minTickGap={40} />
              <YAxis className="text-xs" width={40} allowDecimals={false} />
              <Tooltip contentStyle={tooltipStyle} labelFormatter={(v: string) => new Date(v).toLocaleString()} />
              <Legend wrapperStyle={{ fontSize: 12 }} />
              {(Object.keys(CLASS_COLORS) as (keyof typeof CLASS_COLORS)[]).map((k) => (
                <Bar key={k} dataKey={k} stackId="r" fill={CLASS_COLORS[k]} isAnimationActive={false} />
              ))}
            </BarChart>
          </ResponsiveContainer>
        </SectionCard>
        <SectionCard title="Latency">
          <ResponsiveContainer width="100%" height={220}>
            <LineChart data={series} margin={{ top: 12, right: 8, left: 0, bottom: 0 }}>
              <CartesianGrid strokeDasharray="3 3" className="stroke-border" vertical={false} />
              <XAxis dataKey="at" tickFormatter={(v: string) => tick(v, range)} className="text-xs" minTickGap={40} />
              <YAxis className="text-xs" width={64} tickFormatter={(v: number) => ms(v)} />
              <Tooltip contentStyle={tooltipStyle} labelFormatter={(v: string) => new Date(v).toLocaleString()} formatter={(v: number, name: string) => [ms(v), name.replace("_ms", "")]} />
              <Legend wrapperStyle={{ fontSize: 12 }} formatter={(v: string) => v.replace("_ms", "")} />
              {(Object.keys(LATENCY_COLORS) as (keyof typeof LATENCY_COLORS)[]).map((k) => (
                <Line key={k} dataKey={k} stroke={LATENCY_COLORS[k]} dot={false} strokeWidth={2} isAnimationActive={false} />
              ))}
            </LineChart>
          </ResponsiveContainer>
        </SectionCard>
      </div>
      <ByEnvironment data={data.environments} envs={envs} />
    </>
  );
}

function Figures({ totals }: { totals: TrafficTotals }) {
  const errors = totals.classes["5xx"];
  const items: [string, string][] = [
    ["Requests", count(totals.requests)],
    ["p50", ms(totals.p50_ms)],
    ["p95", ms(totals.p95_ms)],
    ["p99", ms(totals.p99_ms)],
    ["5xx", count(errors)],
  ];
  return (
    <div className="grid grid-cols-2 gap-2 sm:grid-cols-5" data-testid="traffic-figures">
      {items.map(([label, value]) => (
        <div key={label} className="rounded-lg border bg-card p-3">
          <p className="text-xs text-muted-foreground">{label}</p>
          <p className="text-lg font-semibold tabular-nums">{value}</p>
        </div>
      ))}
    </div>
  );
}

function ByEnvironment({ data, envs }: { data: Record<string, TrafficTotals>; envs: string[] }) {
  const rows = envs.filter((e) => data[e]);
  return (
    <SectionCard title="By environment">
      <div className="overflow-x-auto">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Environment</TableHead>
              <TableHead className="text-right">Requests</TableHead>
              <TableHead className="text-right">4xx</TableHead>
              <TableHead className="text-right">5xx</TableHead>
              <TableHead className="text-right">p50</TableHead>
              <TableHead className="text-right">p95</TableHead>
              <TableHead className="text-right">p99</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((e) => {
              const t = data[e]!;
              return (
                <TableRow key={e}>
                  <TableCell className="font-medium">{e}</TableCell>
                  <TableCell className="text-right tabular-nums">{count(t.requests)}</TableCell>
                  <TableCell className="text-right tabular-nums">{count(t.classes["4xx"])}</TableCell>
                  <TableCell className="text-right tabular-nums">{count(t.classes["5xx"])}</TableCell>
                  <TableCell className="text-right tabular-nums">{ms(t.p50_ms)}</TableCell>
                  <TableCell className="text-right tabular-nums">{ms(t.p95_ms)}</TableCell>
                  <TableCell className="text-right tabular-nums">{ms(t.p99_ms)}</TableCell>
                </TableRow>
              );
            })}
          </TableBody>
        </Table>
      </div>
      <p className="mt-2 text-xs text-muted-foreground">An offline environment's 503s count as 5xx, and a redirecting one's 302s as 3xx.</p>
    </SectionCard>
  );
}

/** Whether a replica's p95 stands out: at least twice the others' median. */
export function slowest(replicas: { instance: string; p95_ms: number }[]): string {
  if (replicas.length < 2) return "";
  const sorted = [...replicas].sort((a, b) => b.p95_ms - a.p95_ms);
  const rest = sorted.slice(1).map((r) => r.p95_ms).sort((a, b) => a - b);
  const median = rest[Math.floor(rest.length / 2)] ?? 0;
  return median > 0 && sorted[0]!.p95_ms >= 2 * median ? sorted[0]!.instance : "";
}

// ReplicasSection is each replica's traffic across every site, for an
// administrator: a slow node is slow for every site it serves.
export function ReplicasSection() {
  const [range, setRange] = useState<TrafficRange>("24h");
  const { data, error } = useReplicaTraffic(range, true);
  const slow = slowest(data?.replicas ?? []);
  return (
    <SectionCard title="Replicas" action={<SegmentedControl label="Range" value={range} onChange={setRange} options={RANGES} />}>
      {error && <p className="text-sm text-destructive">{error.message}</p>}
      {data && data.replicas.length === 0 && <p className="text-sm text-muted-foreground">No replica has answered a site request in this range.</p>}
      {data && data.replicas.length > 0 && (
        <div className="overflow-x-auto" data-testid="replicas">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Replica</TableHead>
                <TableHead className="text-right">Requests</TableHead>
                <TableHead className="text-right">5xx</TableHead>
                <TableHead className="text-right">p50</TableHead>
                <TableHead className="text-right">p95</TableHead>
                <TableHead className="text-right">p99</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {data.replicas.map((r) => (
                <TableRow key={r.instance}>
                  <TableCell className="font-mono text-xs">
                    {r.instance}
                    {r.instance === slow && (
                      <Badge variant="warning" className="ml-2">
                        Slowest
                      </Badge>
                    )}
                  </TableCell>
                  <TableCell className="text-right tabular-nums">{count(r.requests)}</TableCell>
                  <TableCell className="text-right tabular-nums">{count(r.classes["5xx"])}</TableCell>
                  <TableCell className="text-right tabular-nums">{ms(r.p50_ms)}</TableCell>
                  <TableCell className="text-right tabular-nums">{ms(r.p95_ms)}</TableCell>
                  <TableCell className="text-right tabular-nums">{ms(r.p99_ms)}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
      <p className="mt-2 text-xs text-muted-foreground">Named by the X-Platform-Instance each replica answers as. Figures across every site.</p>
    </SectionCard>
  );
}
