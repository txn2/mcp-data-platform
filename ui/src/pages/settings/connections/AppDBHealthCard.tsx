import { useEffect, useState } from "react";
import { AlertCircle, Check, ChevronDown, ChevronRight, RefreshCw } from "lucide-react";
import {
  useAppDBHealth,
  useAppDBHealthHistory,
  useAppDBHealthSettings,
  type AppDBHealthReport,
  type AppDBHealthSettings,
} from "@/api/admin/hooks";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { cn } from "@/lib/utils";
import { LastSevenDays, StatTiles } from "./health/HealthCharts";
import { Findings, PassedChecks } from "./health/HealthFindings";
import { SlowStatements } from "./health/HealthStatements";
import {
  ago,
  bytes,
  collapsedSummary,
  levelOf,
  passedChecks,
  problemCount,
  shortVersion,
  sinceDay,
} from "./health/healthFormat";

// AppDBHealthCard is an application database's health (#2409, #2528), read
// from its own statistics views when the card opens and judged by the server
// against the alert thresholds: a status, the findings that need attention
// with their actions, the checks that passed, the application's slowest
// statements, each table's size and vacuum state, and the last seven days of
// samples. The alert settings live in Admin > Settings > Application
// databases, which apply to every application database.

const SETTINGS_PATH = `${(import.meta.env.BASE_URL || "/").replace(/\/$/, "")}/admin/settings/application-databases`;

const PILL = {
  healthy: "bg-emerald-500/10 text-emerald-700 dark:text-emerald-300",
  warning: "bg-amber-500/10 text-amber-700 dark:text-amber-300",
  critical: "bg-red-500/10 text-red-700 dark:text-red-300",
} as const;
const DOT = { healthy: "bg-emerald-500", warning: "bg-amber-500", critical: "bg-red-500" } as const;

function StatusPill({ r }: { r: AppDBHealthReport }) {
  const status = r.status ?? "healthy";
  const n = problemCount(r);
  const label = status === "critical" ? `Critical · ${n}` : status === "warning" ? `Needs attention · ${n}` : "Healthy";
  return (
    <span
      className={cn("inline-flex h-6 items-center gap-1.5 rounded-full px-2.5 text-[13px] font-medium", PILL[status])}
      data-testid="health-status"
    >
      <span aria-hidden="true" className={cn("size-1.5 rounded-full", DOT[status])} />
      {label}
    </span>
  );
}

/** useNow ticks every few seconds so "checked 14 s ago" stays true. */
function useNow(stepMs = 5000): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), stepMs);
    return () => clearInterval(t);
  }, [stepMs]);
  return now;
}

export function AppDBHealthCard({ connectionName }: { connectionName: string }) {
  const [open, setOpen] = useState(false);
  const health = useAppDBHealth(connectionName);
  const now = useNow();
  return (
    <Card className="gap-0 overflow-hidden py-0" data-testid="appdb-health">
      <HealthHeader open={open} onToggle={() => setOpen(!open)} health={health} now={now} />
      {open && (
        <div className="space-y-5 p-4">
          {health.isLoading && <p className="text-sm text-muted-foreground">Loading…</p>}
          {health.error && (
            <Alert variant="destructive">
              <AlertCircle />
              <AlertDescription>{health.error.message}</AlertDescription>
            </Alert>
          )}
          {health.data && <HealthBody r={health.data} connection={connectionName} now={now} />}
        </div>
      )}
    </Card>
  );
}

/** HealthHeader is the fold, the title and status, and the summary or the check control. */
function HealthHeader({
  open,
  onToggle,
  health,
  now,
}: {
  open: boolean;
  onToggle: () => void;
  health: ReturnType<typeof useAppDBHealth>;
  now: number;
}) {
  const data = health.data;
  return (
    <div className={cn("flex flex-wrap items-center justify-between gap-3 px-4 py-3", open && "border-b")}>
      <div className="flex items-center gap-2.5">
        <Button
          size="icon-sm"
          variant="ghost"
          aria-label={open ? "Collapse health" : "Expand health"}
          aria-expanded={open}
          onClick={onToggle}
        >
          {open ? <ChevronDown aria-hidden="true" /> : <ChevronRight aria-hidden="true" />}
        </Button>
        <h2 className="text-base font-semibold">Health</h2>
        {data && <StatusPill r={data} />}
      </div>
      {data && !open && <span className="text-[13px] text-muted-foreground">{collapsedSummary(data)}</span>}
      {data && open && (
        <div className="flex items-center gap-3">
          <span className="text-[13px] text-muted-foreground">
            {shortVersion(data.server.version)} · checked {ago(data.generated_at, now)}
          </span>
          <Button size="sm" variant="outline" disabled={health.isFetching} onClick={() => void health.refetch()}>
            <RefreshCw aria-hidden="true" className={cn(health.isFetching && "animate-spin")} />
            Check now
          </Button>
        </div>
      )}
    </div>
  );
}

function HealthBody({ r, connection, now }: { r: AppDBHealthReport; connection: string; now: number }) {
  const { data: settings } = useAppDBHealthSettings();
  const { data: history } = useAppDBHealthHistory(connection, 7);
  const points = history?.points ?? [];
  const latest = points[points.length - 1]?.sample;
  const drift = latest ? { objects: latest.drift_objects, unchecked: latest.drift_unchecked } : undefined;
  const checks = passedChecks(r, settings, drift);
  const hasFindings = r.findings.length > 0;
  return (
    <>
      {r.errors.map((e) => (
        <Alert key={`${e.section}-${e.error}`} variant="destructive">
          <AlertCircle />
          <AlertDescription>
            The {e.section} section could not be read: {e.error}
          </AlertDescription>
        </Alert>
      ))}
      <StatTiles r={r} settings={settings} points={points} />
      {hasFindings ? (
        <div className="space-y-3">
          <Findings r={r} connection={connection} heading={problemCount(r) > 0 ? "Needs attention" : "Worth a look"} />
          <PassedChecks checks={checks} />
        </div>
      ) : (
        <AllClear r={r} checks={checks} />
      )}
      <SlowStatements r={r} />
      {r.tables.length > 0 && <TablesSection r={r} settings={settings} now={now} />}
      <LastSevenDays points={points} settings={settings} />
      <Footer r={r} settings={settings} />
    </>
  );
}

function AllClear({ r, checks }: { r: AppDBHealthReport; checks: string[] }) {
  return (
    <div className="flex gap-3.5 rounded-xl border p-4" data-testid="health-all-clear">
      <span className="flex size-8 shrink-0 items-center justify-center rounded-full bg-emerald-500/10">
        <Check aria-hidden="true" className="size-4 text-emerald-500" />
      </span>
      <div className="space-y-2.5">
        <div className="space-y-0.5">
          <h3 className="text-[15px] font-semibold">Nothing needs attention</h3>
          <p className="text-[13px] text-muted-foreground">
            {r.tables.length === 0
              ? "This database has no tables yet. Health reports on them once a migration creates some."
              : "Every check is under its alert threshold."}
          </p>
        </div>
        <PassedChecks checks={checks} />
      </div>
    </div>
  );
}

function TablesSection({ r, settings, now }: { r: AppDBHealthReport; settings: AppDBHealthSettings | undefined; now: number }) {
  const t = settings?.dead_tuple_percent;
  return (
    <section className="space-y-2" aria-labelledby="health-tables-h">
      <h3 id="health-tables-h" className="text-sm font-semibold">
        Tables
      </h3>
      <Table data-testid="appdb-health-tables">
        <TableHeader>
          <TableRow>
            <TableHead>Table</TableHead>
            <TableHead className="text-right">Size</TableHead>
            <TableHead className="text-right">Rows</TableHead>
            <TableHead className="w-36">Dead rows</TableHead>
            <TableHead>Last vacuum</TableHead>
            <TableHead>Last analyze</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {r.tables.map((tb) => {
            const pct = tb.dead_ratio * 100;
            const judged = tb.live_tuples + tb.dead_tuples >= 1000;
            const level = judged ? levelOf(t, pct) : "";
            return (
              <TableRow key={`${tb.schema}.${tb.name}`}>
                <TableCell className="font-medium">
                  {tb.schema === "public" ? tb.name : `${tb.schema}.${tb.name}`}
                </TableCell>
                <TableCell className="text-right tabular-nums">{bytes(tb.size_bytes)}</TableCell>
                <TableCell className="text-right tabular-nums">{tb.live_tuples.toLocaleString()}</TableCell>
                <TableCell>
                  <div className="flex items-center gap-2">
                    <div className="h-1.5 w-16 rounded-full bg-muted" aria-hidden="true">
                      <div
                        className={cn(
                          "h-full rounded-full",
                          level === "critical" ? "bg-red-500" : level === "warning" ? "bg-amber-500" : "bg-muted-foreground/50",
                        )}
                        style={{ width: `${Math.min(100, pct)}%` }}
                      />
                    </div>
                    <span className="text-xs tabular-nums">{Math.round(pct)}%</span>
                  </div>
                </TableCell>
                <TableCell>{sinceDay(latestOf(tb.last_vacuum, tb.last_autovacuum), now)}</TableCell>
                <TableCell>{sinceDay(latestOf(tb.last_analyze, tb.last_autoanalyze), now)}</TableCell>
              </TableRow>
            );
          })}
        </TableBody>
      </Table>
    </section>
  );
}

/** latestOf is the later of a manual and an automatic run. */
function latestOf(a?: string, b?: string): string | undefined {
  if (!a) return b;
  if (!b) return a;
  return new Date(a).getTime() >= new Date(b).getTime() ? a : b;
}

function Footer({ r, settings }: { r: AppDBHealthReport; settings: AppDBHealthSettings | undefined }) {
  const channels = settings?.channels ?? [];
  const posted =
    channels.length === 0
      ? "Alerts are not posted anywhere yet."
      : `Alerts post to ${channels.map((c) => `#${c}`).join(", ")}.`;
  return (
    <div className="-mx-4 -mb-4 space-y-1 border-t bg-muted/30 px-4 py-3 text-[13px] text-muted-foreground">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <span>{posted} Thresholds apply to every application database.</span>
        <a className="font-medium text-primary hover:underline" href={SETTINGS_PATH}>
          Alert settings
        </a>
      </div>
      {r.extensions.length > 0 && <p className="text-xs">Extensions: {r.extensions.map((e) => e.name).join(", ")}</p>}
    </div>
  );
}
