import { useCallback } from "react";
import { AlertCircle } from "lucide-react";
import {
  useAppDBHealth,
  useAppDBHealthHistory,
  useAppDBHealthSettings,
  useAppDBSignal,
  useSaveAppDBHealthSettings,
  type AppDBHealthReport,
  type AppDBHealthSettings,
  type AppDBSession,
} from "@/api/admin/hooks";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { ChannelsPicker } from "../ChannelsPicker";
import { SaveButton, SaveFeedbackBanners } from "../settingsChrome";
import { ThresholdsRow } from "../StorageSettingsCard";
import { useSettingsDraft } from "../useSettingsDraft";

// AppDBHealthCard is an application database's health (#2409), read from its
// own statistics views when the card opens: the server, connections, each
// table's size and vacuum state, the indexes worth a look, sessions running
// long or idle in a transaction (each can be cancelled or terminated, #2410),
// blocking chains, the statements pg_stat_statements ranks first, and the
// history the health loop kept. The alert settings below it apply to every
// application database.

function bytes(n: number): string {
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let v = n;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${i === 0 ? v : v.toFixed(1)} ${units[i]}`;
}

function when(value?: string): string {
  if (!value) return "never";
  const at = new Date(value);
  return Number.isNaN(at.getTime()) ? value : at.toLocaleString();
}

function Overview({ r }: { r: AppDBHealthReport }) {
  const items: [string, string][] = [
    ["Server", r.server.version],
    ["Size", bytes(r.server.size_bytes)],
    ["Transaction id age", r.server.transaction_id_age.toLocaleString()],
    ["Connections", `${r.connections.total} of ${r.connections.max}`],
    ["Cache hit ratio", `${(r.server.cache_hit_ratio * 100).toFixed(1)}%`],
    ["Extensions", r.extensions.map((e) => e.name).join(", ") || "none"],
  ];
  return (
    <dl className="grid grid-cols-1 gap-x-6 gap-y-1 text-sm sm:grid-cols-2">
      {items.map(([k, v]) => (
        <div key={k} className="flex gap-2">
          <dt className="text-muted-foreground">{k}</dt>
          <dd className="font-medium">{v}</dd>
        </div>
      ))}
    </dl>
  );
}

function Tables({ r }: { r: AppDBHealthReport }) {
  if (r.tables.length === 0) return <p className="text-xs text-muted-foreground">No tables.</p>;
  return (
    <div className="overflow-x-auto">
      <table className="w-full text-xs" data-testid="appdb-health-tables">
        <thead className="text-left text-muted-foreground">
          <tr>
            {["Table", "Size", "Rows", "Dead tuples", "Seq scans", "Index scans", "Last vacuum", "Last analyze"].map((h) => (
              <th key={h} className="pb-1 pr-3 font-normal">
                {h}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {r.tables.map((t) => (
            <tr key={`${t.schema}.${t.name}`} className="border-t">
              <td className="py-1 pr-3 font-mono">
                {t.schema}.{t.name}
              </td>
              <td className="py-1 pr-3">{bytes(t.size_bytes)}</td>
              <td className="py-1 pr-3">{t.live_tuples.toLocaleString()}</td>
              <td className="py-1 pr-3">
                {t.dead_tuples.toLocaleString()} ({(t.dead_ratio * 100).toFixed(0)}%)
              </td>
              <td className="py-1 pr-3">{t.seq_scans.toLocaleString()}</td>
              <td className="py-1 pr-3">{t.index_scans.toLocaleString()}</td>
              <td className="py-1 pr-3">{when(t.last_vacuum ?? t.last_autovacuum)}</td>
              <td className="py-1 pr-3">{when(t.last_analyze ?? t.last_autoanalyze)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function Indexes({ r }: { r: AppDBHealthReport }) {
  const { unused, duplicate, invalid } = r.indexes;
  if (unused.length + duplicate.length + invalid.length === 0) {
    return <p className="text-xs text-muted-foreground">No unused, duplicate or invalid indexes.</p>;
  }
  return (
    <ul className="space-y-0.5 text-xs" data-testid="appdb-health-indexes">
      {unused.map((i) => (
        <li key={`u-${i.schema}.${i.name}`}>
          Unused: <span className="font-mono">{i.schema}.{i.name}</span> on {i.table} ({bytes(i.size_bytes)})
        </li>
      ))}
      {duplicate.map((d) => (
        <li key={`d-${d.schema}.${d.names.join(",")}`}>
          Duplicate: <span className="font-mono">{d.names.join(", ")}</span> on {d.table}
        </li>
      ))}
      {invalid.map((i) => (
        <li key={`i-${i.schema}.${i.name}`} className="text-destructive">
          Invalid: <span className="font-mono">{i.schema}.{i.name}</span> on {i.table}
        </li>
      ))}
    </ul>
  );
}

function Sessions({ name, sessions }: { name: string; sessions: AppDBSession[] }) {
  const signal = useAppDBSignal(name);
  if (sessions.length === 0) {
    return <p className="text-xs text-muted-foreground">No session is running long or idle in a transaction.</p>;
  }
  return (
    <div className="space-y-2">
      {signal.error && (
        <Alert variant="destructive">
          <AlertCircle />
          <AlertDescription>{signal.error.message}</AlertDescription>
        </Alert>
      )}
      <ul className="space-y-2 text-xs" data-testid="appdb-health-sessions">
        {sessions.map((s) => (
          <li key={s.pid} className="rounded border p-2">
            <div className="flex flex-wrap items-center gap-2">
              <span className="font-mono">{s.pid}</span>
              <span>{s.user}</span>
              <span className="font-medium">{s.state}</span>
              <span className="text-muted-foreground">{Math.round(s.state_seconds)} s in this state</span>
              <span className="ml-auto flex gap-1">
                <Button size="sm" variant="outline" disabled={signal.isPending} onClick={() => signal.mutate({ pid: s.pid, signal: "cancel" })}>
                  Cancel
                </Button>
                <Button size="sm" variant="destructive" disabled={signal.isPending} onClick={() => signal.mutate({ pid: s.pid, signal: "terminate" })}>
                  Terminate
                </Button>
              </span>
            </div>
            <pre className="mt-1 max-h-24 overflow-auto whitespace-pre-wrap font-mono text-[11px] text-muted-foreground">{s.query}</pre>
          </li>
        ))}
      </ul>
    </div>
  );
}

function Statements({ r }: { r: AppDBHealthReport }) {
  if (r.statements.note) return <p className="text-xs text-muted-foreground">{r.statements.note}.</p>;
  return (
    <ul className="space-y-1 text-xs">
      {r.statements.top.map((s) => (
        <li key={s.query_id}>
          <span className="text-muted-foreground">
            {s.calls.toLocaleString()} calls, {s.total_ms.toFixed(0)} ms total, {s.mean_ms.toFixed(1)} ms each
          </span>
          <pre className="max-h-16 overflow-auto whitespace-pre-wrap font-mono text-[11px]">{s.query}</pre>
        </li>
      ))}
    </ul>
  );
}

function History({ name }: { name: string }) {
  const { data } = useAppDBHealthHistory(name, 7);
  const points = data?.points ?? [];
  if (points.length === 0) return <p className="text-xs text-muted-foreground">No samples yet.</p>;
  const recent = points.slice(-8).reverse();
  return (
    <table className="w-full text-xs">
      <thead className="text-left text-muted-foreground">
        <tr>
          {["Sampled", "Size", "Transaction id age", "Idle in transaction", "Dead tuples", "Drift"].map((h) => (
            <th key={h} className="pb-1 pr-3 font-normal">
              {h}
            </th>
          ))}
        </tr>
      </thead>
      <tbody>
        {recent.map((p) => (
          <tr key={p.sampled_at} className="border-t">
            <td className="py-1 pr-3">{when(p.sampled_at)}</td>
            <td className="py-1 pr-3">{bytes(p.sample.size_bytes)}</td>
            <td className="py-1 pr-3">{p.sample.transaction_id_age.toLocaleString()}</td>
            <td className="py-1 pr-3">{p.sample.idle_in_transaction}</td>
            <td className="py-1 pr-3">{(p.sample.max_dead_ratio * 100).toFixed(0)}%</td>
            <td className="py-1 pr-3">{p.sample.drift_objects}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="space-y-1.5">
      <div className="text-xs font-medium">{title}</div>
      {children}
    </div>
  );
}

const THRESHOLDS: { key: keyof AppDBHealthSettings; label: string; unit: string }[] = [
  { key: "transaction_id_age", label: "Transaction id age", unit: "transactions" },
  { key: "idle_in_transaction_seconds", label: "Idle in transaction", unit: "seconds" },
  { key: "connections_percent", label: "Connections", unit: "% of max" },
  { key: "invalid_indexes", label: "Invalid indexes", unit: "indexes" },
  { key: "dead_tuple_percent", label: "Dead tuples", unit: "% of a table" },
  { key: "drift_objects", label: "Drift", unit: "objects" },
];

function AlertSettings() {
  const { data } = useAppDBHealthSettings();
  const save = useSaveAppDBHealthSettings();
  const s = useSettingsDraft<AppDBHealthSettings>(data);
  const draft = s.draft;
  const handleSave = useCallback(() => {
    if (!draft) return;
    s.clearError();
    save.mutate(draft, { onSuccess: s.saved, onError: s.failed });
  }, [draft, save, s]);
  if (!draft) return null;
  const num = (key: "interval_seconds" | "history_days" | "long_running_seconds", label: string) => (
    <label className="flex items-center gap-2 text-sm">
      <span className="w-48">{label}</span>
      <Input
        aria-label={label}
        type="number"
        className="h-8 w-28"
        value={draft[key]}
        onChange={(e) => s.edit({ ...draft, [key]: Number(e.target.value) })}
      />
    </label>
  );
  return (
    <div className="space-y-3" data-testid="appdb-health-settings">
      <p className="text-xs text-muted-foreground">
        These apply to every application database. A threshold of 0 is off.
      </p>
      <SaveFeedbackBanners saveError={s.saveError} dirty={s.dirty} />
      {num("interval_seconds", "Sample every (seconds)")}
      {num("history_days", "History days")}
      {num("long_running_seconds", "Long-running after (seconds)")}
      {THRESHOLDS.map((t) => (
        <ThresholdsRow
          key={t.key}
          label={t.label}
          unit={t.unit}
          value={draft[t.key] as AppDBHealthSettings["drift_objects"]}
          disabled={false}
          onChange={(v) => s.edit({ ...draft, [t.key]: v })}
        />
      ))}
      <ChannelsPicker
        selected={draft.channels}
        onChange={(channels) => s.edit({ ...draft, channels })}
        help="A condition over a threshold is posted at its level and posted again when it clears."
      />
      <SaveButton dirty={s.dirty} saving={save.isPending} saveSuccess={s.saveSuccess} onSave={handleSave} />
    </div>
  );
}

export function AppDBHealthCard({ connectionName }: { connectionName: string }) {
  const { data, error, isLoading } = useAppDBHealth(connectionName);
  return (
    <SectionCard
      title="Health"
      collapsible
      defaultOpen={false}
      summary={data ? `${data.sessions.length} session(s) to look at, ${data.indexes.unused.length} unused index(es)` : undefined}
    >
      {isLoading && <p className="text-xs text-muted-foreground">Loading...</p>}
      {error && (
        <Alert variant="destructive">
          <AlertCircle />
          <AlertDescription>{error.message}</AlertDescription>
        </Alert>
      )}
      {data && (
        <div className="space-y-4">
          {data.errors.map((e) => (
            <Alert key={`${e.section}-${e.error}`} variant="destructive">
              <AlertCircle />
              <AlertDescription>
                {e.section}: {e.error}
              </AlertDescription>
            </Alert>
          ))}
          <Overview r={data} />
          <Section title="Tables">
            <Tables r={data} />
          </Section>
          <Section title="Indexes">
            <Indexes r={data} />
          </Section>
          <Section title="Sessions">
            <Sessions name={connectionName} sessions={data.sessions} />
          </Section>
          {data.blocking.length > 0 && (
            <Section title="Blocked">
              <ul className="text-xs">
                {data.blocking.map((b) => (
                  <li key={b.pid}>
                    {b.pid} ({b.user}) waits on {b.blocked_by.join(", ")}
                  </li>
                ))}
              </ul>
            </Section>
          )}
          <Section title="Statements">
            <Statements r={data} />
          </Section>
          <Section title="History">
            <History name={connectionName} />
          </Section>
          <details>
            <summary className="cursor-pointer text-xs font-medium">Alert settings</summary>
            <div className="mt-2">
              <AlertSettings />
            </div>
          </details>
        </div>
      )}
    </SectionCard>
  );
}
