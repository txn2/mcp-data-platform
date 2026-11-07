import type { AppDBHealthReport, AppDBHealthSettings, AppDBThresholds } from "@/api/admin/hooks";

// The words and numbers the Health card (#2528) writes, kept apart from the
// components so each is tested on its own.

/** bytes writes a size in binary units, the unit apart so a tile can shrink it. */
export function bytesParts(n: number): [string, string] {
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let v = n;
  let i = 0;
  while (Math.abs(v) >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  const text = i === 0 ? String(Math.round(v)) : v.toFixed(1).replace(/\.0$/, "");
  return [text, units[i]!];
}

export function bytes(n: number): string {
  const [v, u] = bytesParts(n);
  return `${v} ${u}`;
}

/** plural writes a count with its noun: "1 call", "3 calls". */
export function plural(n: number, noun: string, many = `${noun}s`): string {
  return `${n.toLocaleString()} ${n === 1 ? noun : many}`;
}

/** ms writes a duration in milliseconds the way the table shows it. */
export function ms(v: number): string {
  if (v >= 1000) return `${(v / 1000).toFixed(1).replace(/\.0$/, "")} s`;
  if (v >= 10) return `${Math.round(v)} ms`;
  return `${v.toFixed(1).replace(/\.0$/, "")} ms`;
}

/** shortVersion is "PostgreSQL 17.5" from the server_version setting. */
export function shortVersion(version: string): string {
  const m = version.match(/^\d+(\.\d+)?/);
  return m ? `PostgreSQL ${m[0]}` : version;
}

/** ago writes how long ago a time was: "just now", "14 s ago", "3 min ago". */
export function ago(iso: string, now: number): string {
  const secs = Math.max(0, Math.round((now - new Date(iso).getTime()) / 1000));
  if (Number.isNaN(secs)) return "";
  if (secs < 5) return "just now";
  if (secs < 60) return `${secs} s ago`;
  const mins = Math.round(secs / 60);
  if (mins < 60) return `${mins} min ago`;
  return `${Math.round(mins / 60)} h ago`;
}

/** sinceDay writes a table's last vacuum or analyze: "2 h ago", "Yesterday", "Never". */
export function sinceDay(iso: string | undefined, now: number): string {
  if (!iso) return "Never";
  const at = new Date(iso).getTime();
  if (Number.isNaN(at)) return "Never";
  const hours = (now - at) / 3_600_000;
  if (hours < 1) return `${Math.max(1, Math.round(hours * 60))} min ago`;
  if (hours < 24) return `${Math.round(hours)} h ago`;
  if (hours < 48) return "Yesterday";
  return `${Math.round(hours / 24)} days ago`;
}

/** levelOf is the level a value reaches against thresholds, "" below both or off. */
export function levelOf(t: AppDBThresholds | undefined, v: number): "" | "warning" | "critical" {
  if (!t) return "";
  if (t.critical > 0 && v >= t.critical) return "critical";
  if (t.warning > 0 && v >= t.warning) return "warning";
  return "";
}

/** firstThreshold is the lowest threshold that is on, 0 when both are off. */
export function firstThreshold(t: AppDBThresholds | undefined): number {
  if (!t) return 0;
  if (t.warning > 0) return t.warning;
  return t.critical;
}

/** collapsedSummary is the one line a folded card states. */
export function collapsedSummary(r: AppDBHealthReport): string {
  const cache = `cache ${Math.round(r.server.cache_hit_ratio * 1000) / 10}%`;
  return `${bytes(r.server.size_bytes)} · ${r.connections.total} of ${r.connections.max} connections · ${cache}`;
}

/** problemCount is how many findings are past an alert threshold. */
export function problemCount(r: AppDBHealthReport): number {
  return r.findings.filter((f) => f.level !== "info").length;
}

/** passedChecks names what the report found nothing wrong with. */
export function passedChecks(
  r: AppDBHealthReport,
  s: AppDBHealthSettings | undefined,
  drift: { objects: number; unchecked: number } | undefined,
): string[] {
  return [...indexChecks(r), ...sessionChecks(r), ...deadRowCheck(r, s), ...driftCheck(drift)];
}

function indexChecks(r: AppDBHealthReport): string[] {
  const { unused, duplicate, invalid } = r.indexes;
  const out: string[] = [];
  if (invalid.length === 0 && duplicate.length === 0) out.push("Indexes valid, none duplicated");
  if (unused.length === 0) out.push("No unused indexes");
  return out;
}

function sessionChecks(r: AppDBHealthReport): string[] {
  const out: string[] = [];
  if (r.blocking.length === 0) out.push("No blocked sessions");
  if (r.sessions.length === 0) out.push("No long-running or idle sessions");
  return out;
}

function deadRowCheck(r: AppDBHealthReport, s: AppDBHealthSettings | undefined): string[] {
  const dead = firstThreshold(s?.dead_tuple_percent);
  if (r.tables.length === 0 || dead <= 0 || r.findings.some((f) => f.kind === "dead_tuples")) return [];
  return [`Dead rows under ${dead}% everywhere`];
}

function driftCheck(drift: { objects: number; unchecked: number } | undefined): string[] {
  return drift && drift.objects === 0 && drift.unchecked === 0 ? ["Schema matches migrations"] : [];
}

// KEYWORDS are the clauses a formatted statement starts a line at.
const CLAUSES = [
  "SELECT", "FROM", "WHERE", "GROUP BY", "ORDER BY", "HAVING", "LIMIT", "OFFSET", "VALUES", "SET",
  "RETURNING", "LEFT JOIN", "RIGHT JOIN", "INNER JOIN", "FULL JOIN", "JOIN", "ON CONFLICT", "UNION ALL", "UNION",
  "INSERT INTO", "UPDATE", "DELETE FROM", "WITH",
];

const KEYWORD = /\b(SELECT|FROM|WHERE|GROUP BY|ORDER BY|HAVING|LIMIT|OFFSET|VALUES|SET|RETURNING|LEFT JOIN|RIGHT JOIN|INNER JOIN|FULL JOIN|JOIN|ON CONFLICT|ON|AND|OR|NOT|IN|IS|NULL|AS|DESC|ASC|UNION ALL|UNION|INSERT INTO|UPDATE|DELETE FROM|WITH|DISTINCT|CASE|WHEN|THEN|ELSE|END|COUNT|BY)\b/gi;

/** formatSQL breaks a statement onto a line per clause, outside quoted text. */
export function formatSQL(sql: string): string {
  const flat = sql.replace(/\s+/g, " ").trim();
  if (/'|"|\$\$/.test(flat)) return flat;
  const clause = new RegExp(`\\s+(?=\\b(?:${CLAUSES.join("|")})\\b)`, "gi");
  return flat.replace(clause, "\n");
}

/** sqlTokens splits a statement into keyword and other runs for highlighting. */
export function sqlTokens(sql: string): { text: string; keyword: boolean }[] {
  const out: { text: string; keyword: boolean }[] = [];
  let last = 0;
  for (const m of sql.matchAll(KEYWORD)) {
    if (m.index > last) out.push({ text: sql.slice(last, m.index), keyword: false });
    out.push({ text: m[0], keyword: true });
    last = m.index + m[0].length;
  }
  if (last < sql.length) out.push({ text: sql.slice(last), keyword: false });
  return out;
}
