import { useMemo } from "react";
import {
  useObservabilityQuery,
  useObservabilityQueryRange,
  isBackendUnconfigured,
} from "@/api/observability/hooks";
import {
  NODE_QUERIES,
  NODE_RANGE_QUERIES,
  mergeNodeMetrics,
  seriesByNode,
  type NodeHealth,
  type TrendPoint,
} from "./health";
import { TrendArea } from "@/components/charts/TrendArea";
import { Server } from "lucide-react";

const CPU_COLOR = "hsl(199, 89%, 48%)";
const MEM_COLOR = "hsl(262, 83%, 62%)";

function bytesAxis(bytes: number): string {
  const mib = bytes / (1024 * 1024);
  return mib >= 1024 ? `${(mib / 1024).toFixed(1)}G` : `${Math.round(mib)}M`;
}

// HealthView shows one full-width status band PER node (pod). Installs
// typically run 1-3 nodes, so each node gets generous space rather than a
// compact table: a status-colored rail + prominent identity on the left,
// then the node's runtime metrics spread across the width as large stat
// blocks. Metrics are per-node (not fleet aggregates), read from Prometheus
// via the observability proxy; a 503 (no metrics backend) degrades to a
// single empty state, and any missing metric renders as "-". MCP and Events
// tabs are unaffected since they read the audit database.

const RECENT_RESTART_SECONDS = 10 * 60;

const EMERALD = "hsl(142, 71%, 45%)";
const AMBER = "hsl(38, 92%, 50%)";

function fmtUptime(seconds: number | undefined): { value: string; unit?: string } {
  if (seconds === undefined) return { value: "-" };
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  if (h >= 24) return { value: `${Math.floor(h / 24)}d ${h % 24}h` };
  if (h >= 1) return { value: `${h}h ${m}m` };
  return { value: `${m}m` };
}

function fmtBytes(bytes: number | undefined): { value: string; unit?: string } {
  if (bytes === undefined) return { value: "-" };
  const mib = bytes / (1024 * 1024);
  if (mib >= 1024) return { value: (mib / 1024).toFixed(1), unit: "GiB" };
  return { value: String(Math.round(mib)), unit: "MiB" };
}

function fmtCores(cores: number | undefined): { value: string; unit?: string } {
  return cores === undefined ? { value: "-" } : { value: cores.toFixed(2), unit: "cores" };
}

function fmtInt(n: number | undefined): { value: string; unit?: string } {
  return n === undefined ? { value: "-" } : { value: Math.round(n).toLocaleString() };
}

function StatBlock({
  label,
  parts,
  accent,
}: {
  label: string;
  parts: { value: string; unit?: string };
  accent?: string;
}) {
  return (
    <div>
      <p className="text-[11px] font-medium uppercase tracking-wider text-muted-foreground">{label}</p>
      <p className="mt-1.5 flex items-baseline gap-1">
        <span className="tabular-nums text-2xl font-semibold" style={accent ? { color: accent } : undefined}>
          {parts.value}
        </span>
        {parts.unit && <span className="text-xs text-muted-foreground">{parts.unit}</span>}
      </p>
    </div>
  );
}

// splitNodeName separates the common prefix from the unique suffix (the
// last "-" segment, e.g. the pod's random suffix), which is the part that
// actually distinguishes nodes. The suffix is emphasized in the UI.
function splitNodeName(label: string): { prefix: string; suffix: string } {
  const i = label.lastIndexOf("-");
  if (i <= 0 || i === label.length - 1) return { prefix: "", suffix: label };
  return { prefix: label.slice(0, i + 1), suffix: label.slice(i + 1) };
}

function NodeRow({
  node,
  cpuSeries,
  memSeries,
}: {
  node: NodeHealth;
  cpuSeries: TrendPoint[];
  memSeries: TrendPoint[];
}) {
  const restarted = node.uptime !== undefined && node.uptime < RECENT_RESTART_SECONDS;
  const accent = restarted ? AMBER : EMERALD;
  const { prefix, suffix } = splitNodeName(node.label);

  return (
    <div
      className="relative overflow-hidden rounded-xl border bg-card transition-colors"
      style={{ borderLeft: `3px solid ${accent}` }}
    >
      {/* Subtle status-tinted wash for depth. */}
      <div
        className="pointer-events-none absolute inset-x-0 top-0 h-px"
        style={{ background: `linear-gradient(to right, ${accent}55, transparent)` }}
      />
      <div className="relative space-y-5 p-6">
        {/* Full-width identity header: the whole pod name, no truncation,
            with the unique suffix emphasized over the shared prefix. */}
        <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2">
          <div className="flex items-center gap-2.5">
            <Server className="h-4 w-4 shrink-0" style={{ color: accent }} />
            <span className="break-all font-mono text-base">
              <span className="text-muted-foreground">{prefix}</span>
              <span className="font-semibold text-foreground">{suffix}</span>
            </span>
          </div>
          <span
            className="inline-flex shrink-0 items-center gap-1.5 rounded-full px-2.5 py-1 text-xs font-medium"
            style={{ backgroundColor: `${accent}1f`, color: accent }}
          >
            <span className="h-1.5 w-1.5 rounded-full" style={{ backgroundColor: accent }} />
            {restarted ? "Restarted recently" : "Healthy"}
          </span>
        </div>

        {/* Metrics spread across the full width below the name. */}
        <div className="grid grid-cols-2 gap-x-6 gap-y-5 sm:grid-cols-3 lg:grid-cols-6">
          <StatBlock label="Uptime" parts={fmtUptime(node.uptime)} accent={restarted ? AMBER : undefined} />
          <StatBlock label="CPU" parts={fmtCores(node.cpu)} />
          <StatBlock label="Memory" parts={fmtBytes(node.rss)} />
          <StatBlock label="Heap" parts={fmtBytes(node.heap)} />
          <StatBlock label="Goroutines" parts={fmtInt(node.goroutines)} />
          <StatBlock label="In-flight" parts={fmtInt(node.inflight)} />
        </div>

        {/* CPU and memory trend charts for this node. */}
        <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
          <div className="rounded-lg border bg-background/40 p-3">
            <p className="mb-1 text-[11px] font-medium uppercase tracking-wider text-muted-foreground">
              CPU (cores)
            </p>
            <TrendArea data={cpuSeries} color={CPU_COLOR} format={(v) => v.toFixed(2)} />
          </div>
          <div className="rounded-lg border bg-background/40 p-3">
            <p className="mb-1 text-[11px] font-medium uppercase tracking-wider text-muted-foreground">
              Memory (RSS)
            </p>
            <TrendArea data={memSeries} color={MEM_COLOR} format={bytesAxis} />
          </div>
        </div>
      </div>
    </div>
  );
}

export function HealthView() {
  const uptime = useObservabilityQuery(NODE_QUERIES.uptime);
  const cpu = useObservabilityQuery(NODE_QUERIES.cpu);
  const rss = useObservabilityQuery(NODE_QUERIES.rss);
  const heap = useObservabilityQuery(NODE_QUERIES.heap);
  const goroutines = useObservabilityQuery(NODE_QUERIES.goroutines);
  const inflight = useObservabilityQuery(NODE_QUERIES.inflight);

  // Per-node CPU/memory trend over the last hour. Snap to the minute so the
  // range-query keys stay stable across re-renders.
  const win = useMemo(() => {
    const end = Math.floor(Date.now() / 60_000) * 60;
    return { start: end - 3600, end };
  }, []);
  const cpuRange = useObservabilityQueryRange(NODE_RANGE_QUERIES.cpu, win.start, win.end, 60);
  const memRange = useObservabilityQueryRange(NODE_RANGE_QUERIES.memory, win.start, win.end, 60);

  if (isBackendUnconfigured(uptime.error)) {
    return (
      <div className="rounded-xl border border-dashed bg-card p-10 text-center">
        <h3 className="text-sm font-medium">Node metrics unavailable</h3>
        <p className="mt-2 text-sm text-muted-foreground">
          Per-node health metrics are not available for this platform right now.
        </p>
      </div>
    );
  }

  const isLoading =
    uptime.isLoading || cpu.isLoading || rss.isLoading || heap.isLoading || goroutines.isLoading || inflight.isLoading;

  const nodes = mergeNodeMetrics([
    { key: "uptime", resp: uptime.data },
    { key: "cpu", resp: cpu.data },
    { key: "rss", resp: rss.data },
    { key: "heap", resp: heap.data },
    { key: "goroutines", resp: goroutines.data },
    { key: "inflight", resp: inflight.data },
  ]);

  if (!isLoading && nodes.length === 0) {
    return (
      <div className="rounded-xl border border-dashed bg-card p-10 text-center text-sm text-muted-foreground">
        No nodes are currently reporting metrics.
      </div>
    );
  }

  const cpuByNode = seriesByNode(cpuRange.data);
  const memByNode = seriesByNode(memRange.data);

  return (
    <div className="space-y-4">
      <div className="flex items-baseline justify-between">
        <h2 className="text-sm font-medium">Nodes</h2>
        <span className="text-xs text-muted-foreground">
          {nodes.length} reporting · per-node runtime health
        </span>
      </div>
      <div className="space-y-4">
        {nodes.map((node) => (
          <NodeRow
            key={node.id}
            node={node}
            cpuSeries={cpuByNode.get(node.id) ?? []}
            memSeries={memByNode.get(node.id) ?? []}
          />
        ))}
      </div>
    </div>
  );
}
