import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { ArrowLeft } from "lucide-react";
import {
  Bar,
  BarChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import { adminEndpointsKey, useAdminEndpoint } from "@/api/admin/hooks";
import type {
  BotCheckProvider,
  AdminEndpointDetail,
  EndpointBucket,
  EndpointRange,
} from "@/api/admin/types";
import { useSetScriptEndpointEnabled } from "@/api/portal/hooks/scriptEndpoint";
import { FilterSelect } from "@/components/patterns/FilterSelect";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { bucketLabel } from "@/pages/webhooks/historySeries";
import { formatWhen } from "../runFormat";
import {
  ENDPOINTS_PATH,
  PROVIDER_LABELS,
  PUBLIC_NOTE,
  callersSummary,
  formatMs,
  refusalLabel,
} from "./endpointLabels";
import { AccessBadge, RANGE_OPTIONS } from "./EndpointsTab";

// EndpointDetailPage is one endpoint (#2208): its binding, its automation,
// the requests it answered over the range by status class beside the ones it
// refused before its handler ran, how long they took, its busiest authenticated
// callers, and the requests that failed. An administrator turns it on and off
// here, and edits it on its automation's page, the one form that sets a
// binding.

const SERIES: { key: keyof EndpointBucket; label: string; color: string }[] = [
  { key: "2xx", label: "2xx", color: "hsl(142, 71%, 45%)" },
  { key: "3xx", label: "3xx", color: "hsl(199, 89%, 48%)" },
  { key: "4xx", label: "4xx", color: "hsl(38, 92%, 50%)" },
  { key: "5xx", label: "5xx", color: "hsl(0, 72%, 51%)" },
  { key: "refused", label: "Refused", color: "hsl(215, 16%, 57%)" },
];

export function EndpointDetailPage({
  name,
  onNavigate,
}: {
  name: string;
  onNavigate: (path: string) => void;
}) {
  const [range, setRange] = useState<EndpointRange>("day");
  const { data, isLoading, isError } = useAdminEndpoint(name, range);
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-2">
        <Button
          variant="ghost"
          size="sm"
          onClick={() => onNavigate(ENDPOINTS_PATH)}
        >
          <ArrowLeft className="size-4" /> All endpoints
        </Button>
        <div className="ml-auto">
          <FilterSelect
            label="Range"
            value={range}
            onChange={(v) => setRange((v || "day") as EndpointRange)}
            options={RANGE_OPTIONS}
          />
        </div>
      </div>
      {isError && (
        <Alert variant="destructive">
          <AlertDescription>This endpoint could not be read.</AlertDescription>
        </Alert>
      )}
      {isLoading && <p className="text-sm text-muted-foreground">Loading...</p>}
      {data && <EndpointBody endpoint={data} onNavigate={onNavigate} />}
    </div>
  );
}

function EndpointBody({
  endpoint: e,
  onNavigate,
}: {
  endpoint: AdminEndpointDetail;
  onNavigate: (path: string) => void;
}) {
  const scriptPath = `/admin/automations/${encodeURIComponent(e.script.id)}`;
  return (
    <>
      <div className="space-y-1">
        <div className="flex flex-wrap items-center gap-2">
          <h2 className="font-mono text-lg font-semibold">{e.name}</h2>
          <AccessBadge endpoint={e} />
          <span className="text-sm text-muted-foreground">
            {e.enabled ? "On" : "Off"}
          </span>
        </div>
        <p className="break-all font-mono text-xs text-muted-foreground">
          {e.methods.join(", ")} {e.url}
          {e.path}
        </p>
        <AccessLine endpoint={e} />
        <p className="text-sm">
          Runs{" "}
          <button
            type="button"
            className="font-medium underline-offset-4 hover:underline"
            onClick={() => onNavigate(scriptPath)}
          >
            {e.script.display_name || e.script.name || e.script.id}
          </button>
          {e.script.owner && (
            <span className="text-muted-foreground">
              , owned by {e.script.owner}
            </span>
          )}
        </p>
      </div>
      <EndpointActions endpoint={e} onEdit={() => onNavigate(scriptPath)} />
      <Figures endpoint={e} />
      <SectionCard title="Requests">
        <RequestChart buckets={e.series} bucketSeconds={e.bucket_seconds} />
      </SectionCard>
      <div className="grid gap-4 md:grid-cols-2">
        <SectionCard title="Refused before the handler">
          <Refusals byReason={e.refused_by_reason} />
        </SectionCard>
        {e.auth_mode === "authenticated" && (
          <SectionCard title="Top callers">
            <TopCallers callers={e.top_callers} />
          </SectionCard>
        )}
      </div>
      <SectionCard title="Failed requests">
        <Failures failures={e.failures} />
      </SectionCard>
    </>
  );
}

/**
 * AccessLine says who the endpoint answers (#2259): an authenticated one's
 * callers, a bot_check one's check, its provider and the methods it checks,
 * or that a public one answers anyone with no check.
 */
function AccessLine({ endpoint: e }: { endpoint: AdminEndpointDetail }) {
  let text: string;
  switch (e.auth_mode) {
    case "authenticated":
      text = `Admits: ${callersSummary(e.callers)}.`;
      break;
    case "bot_check": {
      const provider = e.bot_check_provider
        ? PROVIDER_LABELS[e.bot_check_provider as BotCheckProvider] ?? e.bot_check_provider
        : "";
      const methods = e.checked_methods.length === 0 ? "every method" : e.checked_methods.join(", ");
      text = `Requires the bot check ${e.bot_check}${provider ? ` (${provider})` : ""} on ${methods}.`;
      break;
    }
    default:
      text = PUBLIC_NOTE;
  }
  return (
    <p className="text-sm" data-testid="endpoint-access">
      {text}
    </p>
  );
}

function EndpointActions({
  endpoint: e,
  onEdit,
}: {
  endpoint: AdminEndpointDetail;
  onEdit: () => void;
}) {
  const queryClient = useQueryClient();
  const setEnabled = useSetScriptEndpointEnabled(e.script.id);
  const [error, setError] = useState<string | null>(null);
  const toggle = () => {
    setError(null);
    setEnabled.mutate(!e.enabled, {
      onSuccess: () =>
        queryClient.invalidateQueries({ queryKey: adminEndpointsKey }),
      onError: (err) =>
        setError(
          err instanceof Error ? err.message : "The change could not be saved",
        ),
    });
  };
  return (
    <div className="space-y-2">
      <div className="flex flex-wrap gap-2">
        <Button
          size="sm"
          variant="outline"
          disabled={setEnabled.isPending}
          onClick={toggle}
        >
          {e.enabled ? "Turn off" : "Turn on"}
        </Button>
        <Button size="sm" variant="outline" onClick={onEdit}>
          Edit
        </Button>
      </div>
      {error && (
        <p role="alert" className="text-xs text-destructive">
          {error}
        </p>
      )}
    </div>
  );
}

function Figures({ endpoint: e }: { endpoint: AdminEndpointDetail }) {
  const t = e.traffic;
  const figures: [string, string][] = [
    ["Requests", t.requests.toLocaleString()],
    ["Failures", t.failures.toLocaleString()],
    ["Refused", e.refused.toLocaleString()],
    ["p50", formatMs(t.p50_ms)],
    ["p95", formatMs(t.p95_ms)],
    ["Callers", t.callers.toLocaleString()],
    ["Last request", t.last_request_at ? formatWhen(t.last_request_at) : "-"],
  ];
  return (
    <dl className="grid grid-cols-2 gap-3 sm:grid-cols-4 lg:grid-cols-7">
      {figures.map(([label, value]) => (
        <div key={label} className="rounded-md border p-3">
          <dt className="text-xs text-muted-foreground">{label}</dt>
          <dd className="text-lg font-semibold tabular-nums">{value}</dd>
        </div>
      ))}
    </dl>
  );
}

/** RequestChart is one stacked bar per bucket: answered requests by status
 * class, and the refused ones in grey beside them. */
export function RequestChart({
  buckets,
  bucketSeconds,
}: {
  buckets: EndpointBucket[];
  bucketSeconds: number;
}) {
  const total = buckets.reduce(
    (n, b) => n + b["2xx"] + b["3xx"] + b["4xx"] + b["5xx"] + b.refused,
    0,
  );
  if (total === 0) {
    return (
      <div className="flex h-48 items-center justify-center rounded-md border border-dashed text-sm text-muted-foreground">
        No requests in this range.
      </div>
    );
  }
  const fmt = (iso: string) => bucketLabel(iso, bucketSeconds);
  return (
    <ResponsiveContainer width="100%" height={220}>
      <BarChart
        data={buckets}
        margin={{ top: 4, right: 8, left: 0, bottom: 0 }}
      >
        <XAxis
          dataKey="start"
          tickFormatter={fmt}
          className="text-xs"
          tick={{ fill: "hsl(var(--muted-foreground))" }}
          minTickGap={40}
        />
        <YAxis
          className="text-xs"
          tick={{ fill: "hsl(var(--muted-foreground))" }}
          width={44}
          allowDecimals={false}
        />
        <Tooltip
          contentStyle={{
            backgroundColor: "hsl(var(--card))",
            border: "1px solid hsl(var(--border))",
            borderRadius: "0.375rem",
            fontSize: "0.75rem",
          }}
          labelFormatter={(v: string) => fmt(v)}
        />
        {SERIES.map((s) => (
          <Bar
            key={s.key}
            dataKey={s.key}
            name={s.label}
            stackId="requests"
            fill={s.color}
            isAnimationActive={false}
          />
        ))}
      </BarChart>
    </ResponsiveContainer>
  );
}

function Refusals({ byReason }: { byReason: Record<string, number> }) {
  const rows = Object.entries(byReason).sort((a, b) => b[1] - a[1]);
  if (rows.length === 0)
    return (
      <p className="text-sm text-muted-foreground">
        Nothing was refused in this range.
      </p>
    );
  return (
    <Table>
      <TableBody>
        {rows.map(([reason, n]) => (
          <TableRow key={reason}>
            <TableCell>{refusalLabel(reason)}</TableCell>
            <TableCell className="text-right tabular-nums">
              {n.toLocaleString()}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

function TopCallers({
  callers,
}: {
  callers: AdminEndpointDetail["top_callers"];
}) {
  if (callers.length === 0)
    return (
      <p className="text-sm text-muted-foreground">
        No authenticated caller in this range.
      </p>
    );
  return (
    <Table>
      <TableBody>
        {callers.map((c) => (
          <TableRow key={c.caller}>
            <TableCell className="text-xs">{c.caller}</TableCell>
            <TableCell className="text-right tabular-nums">
              {c.requests.toLocaleString()}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

function Failures({ failures }: { failures: AdminEndpointDetail["failures"] }) {
  if (failures.length === 0)
    return (
      <p className="text-sm text-muted-foreground">
        No failed request in the last 7 days.
      </p>
    );
  return (
    <div className="overflow-x-auto">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>When</TableHead>
            <TableHead>Method</TableHead>
            <TableHead>Status</TableHead>
            <TableHead>Version</TableHead>
            <TableHead>Duration</TableHead>
            <TableHead>Error</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {failures.map((f) => (
            <TableRow key={f.id}>
              <TableCell className="text-xs">
                {formatWhen(f.created_at)}
              </TableCell>
              <TableCell className="font-mono text-xs">{f.method}</TableCell>
              <TableCell className="tabular-nums">{f.status}</TableCell>
              <TableCell className="tabular-nums">{f.version}</TableCell>
              <TableCell className="tabular-nums">
                {formatMs(f.duration_ms)}
              </TableCell>
              <TableCell className="max-w-md truncate text-xs" title={f.error}>
                {f.error}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}
