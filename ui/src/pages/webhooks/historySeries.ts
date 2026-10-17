import type { WebhookHistory, WebhookStatusOverview } from "@/api/admin/types";

// The webhooks page's history (#2150) as the rows its charts draw: one row per
// bucket, a column per series, every bucket of the range present so a quiet
// stretch reads as zero rather than as a gap.

export type Row = { at: string } & Record<string, number | string>;

/** bucketTimes is the start of every bucket of the overview's range. */
export function bucketTimes(data: WebhookStatusOverview): string[] {
  const step = data.bucket_seconds * 1000;
  if (step <= 0) return [];
  const first = Math.floor(Date.parse(data.from) / step) * step;
  const last = Date.parse(data.until || data.generated_at);
  const out: string[] = [];
  for (let t = first; t < last; t += step) out.push(new Date(t).toISOString());
  return out;
}

/** rowsOf is one zeroed row per bucket with the given columns. */
function rowsOf(data: WebhookStatusOverview, columns: string[]): Map<string, Row> {
  const rows = new Map<string, Row>();
  for (const at of bucketTimes(data)) {
    const row: Row = { at };
    for (const c of columns) row[c] = 0;
    rows.set(at, row);
  }
  return rows;
}

/** key is a series point's time as a bucket key. */
function key(at: string): string {
  return new Date(at).toISOString();
}

/** add adds n to a row's column, making the row when the bucket is not one of the range's. */
function add(rows: Map<string, Row>, at: string, column: string, n: number) {
  const k = key(at);
  const row = rows.get(k) ?? { at: k };
  row[column] = ((row[column] as number | undefined) ?? 0) + n;
  rows.set(k, row);
}

function sorted(rows: Map<string, Row>): Row[] {
  return [...rows.values()].sort((a, b) => a.at.localeCompare(b.at));
}

/** statusLabel is an HTTP status as the page names it. */
export function statusLabel(status: number): string {
  return status === 0 ? "No answer" : String(status);
}

/** statusSeries is the requests by status per bucket, for one source or all. */
export function statusSeries(data: WebhookStatusOverview, source: string): { rows: Row[]; statuses: string[] } {
  const points = data.history.statuses.filter((p) => !source || p.source === source);
  const statuses = [...new Set(points.map((p) => statusLabel(p.http_status)))].sort();
  const rows = rowsOf(data, statuses);
  for (const p of points) add(rows, p.at, statusLabel(p.http_status), p.count);
  return { rows: sorted(rows), statuses };
}

/** replicaNames is every receiver the history names. */
export function replicaNames(history: WebhookHistory): string[] {
  return [...new Set(history.replicas.map((r) => r.replica))].sort();
}

/** flowSeries is each receiver's events received and written per bucket:
 * `<replica> received` and `<replica> written`. */
export function flowSeries(data: WebhookStatusOverview): Row[] {
  const names = replicaNames(data.history);
  const rows = rowsOf(data, names.flatMap((n) => [`${n} received`, `${n} written`]));
  for (const r of data.history.replicas) {
    add(rows, r.at, `${r.replica} received`, r.received_events);
    add(rows, r.at, `${r.replica} written`, r.drained_events);
  }
  return sorted(rows);
}

/** spoolSeries is each receiver's spool depth (events) and oldest age (seconds) per bucket. */
export function spoolSeries(data: WebhookStatusOverview): Row[] {
  const names = replicaNames(data.history);
  const rows = rowsOf(data, names.flatMap((n) => [`${n} events`, `${n} oldest`]));
  for (const r of data.history.replicas) {
    const row = rows.get(key(r.at)) ?? { at: key(r.at) };
    row[`${r.replica} events`] = Math.max((row[`${r.replica} events`] as number | undefined) ?? 0, r.spool_events);
    row[`${r.replica} oldest`] = Math.max((row[`${r.replica} oldest`] as number | undefined) ?? 0, r.oldest_age_seconds);
    rows.set(key(r.at), row);
  }
  return sorted(rows);
}

/** ackSeries is the acknowledgement per bucket for one source or all: the
 * worst p50 and p99 of the sources, and each stage's average weighted by
 * deliveries. A bucket with no deliveries has no latency. */
export function ackSeries(data: WebhookStatusOverview, source: string): Row[] {
  const byAt = new Map<string, { n: number; p50: number; p99: number; buffer: number; mark: number; put: number }>();
  for (const p of data.history.ack) {
    if (source && p.source !== source) continue;
    const k = key(p.at);
    const cur = byAt.get(k) ?? { n: 0, p50: 0, p99: 0, buffer: 0, mark: 0, put: 0 };
    cur.n += p.count;
    cur.p50 = Math.max(cur.p50, p.p50_ms);
    cur.p99 = Math.max(cur.p99, p.p99_ms);
    cur.buffer += p.buffer_ms * p.count;
    cur.mark += p.mark_ms * p.count;
    cur.put += p.put_ms * p.count;
    byAt.set(k, cur);
  }
  return [...byAt.entries()]
    .filter(([, v]) => v.n > 0)
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([at, v]) => ({
      at, p50: v.p50, p99: v.p99, buffer: v.buffer / v.n, mark: v.mark / v.n, put: v.put / v.n,
    }));
}

/** compactionSeries is each compaction, oldest first, for one source or all:
 * its lag behind the window's end and its duration, in seconds. */
export interface CompactionPoint {
  at: string;
  source: string;
  lag: number;
  duration: number;
}

export function compactionSeries(data: WebhookStatusOverview, source: string): CompactionPoint[] {
  return data.history.compactions
    .filter((c) => !source || c.source === source)
    .map((c) => ({ at: key(c.at), source: c.source, lag: c.lag_seconds, duration: c.duration_ms / 1000 }))
    .sort((a, b) => a.at.localeCompare(b.at));
}

/** bucketLabel is a bucket's time as the axis shows it: the time of day for
 * a range within a day, the day and hour past it. */
export function bucketLabel(iso: string, bucketSeconds: number): string {
  const d = new Date(iso);
  if (bucketSeconds >= 3600) {
    return d.toLocaleString([], { weekday: "short", hour: "2-digit", minute: "2-digit" });
  }
  return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}
