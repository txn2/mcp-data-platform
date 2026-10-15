import { useMemo, useState } from "react";
import { Plus, Webhook } from "lucide-react";
import { useWebhookStatus } from "@/api/admin/hooks";
import type {
  WebhookReplicaSpool,
  WebhookSourceRejection,
  WebhookSourceStatus,
  WebhookStatusOverview,
  WebhookStatusRange,
} from "@/api/admin/types";
import { EmptyState } from "@/components/patterns/EmptyState";
import { SectionCard } from "@/components/patterns/SectionCard";
import { relativeTime } from "@/components/provenance/parts";
import { formatBytes } from "@/lib/format";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { RejectionsTable } from "./RejectionsTable";
import { VolumeChart } from "./VolumeChart";
import {
  HEALTH_HINTS,
  HEALTH_LABELS,
  HEALTH_VARIANTS,
  formatMs,
  outcomeLabel,
  splitCounts,
  volumeSeries,
} from "./webhookOverview";

// WebhooksPage is the overview of every inbound webhook source (#1870, #1979),
// read in one request: the request volume by outcome, each source's health,
// counts and response time, and the newest rejected requests across all of
// them. Choosing a source narrows the chart and the rejections to it; the
// rejections are then read for that source alone (#2107), so another
// source's volume never hides them. A row opens the source's page, which is
// where its address and settings are.

const RANGES: { value: WebhookStatusRange; label: string }[] = [
  { value: "hour", label: "Last hour" },
  { value: "day", label: "Last 24 hours" },
];

// ALL is the source picker's value for every source; a Radix select item
// cannot carry an empty value.
const ALL = "__all__";

/** useChosenSource is the source picked in the chart, or "" for every source
 * (one deleted while picked falls back to every source), and the rejections
 * to list: every source's from the overview, or the picked source's read for
 * it alone. */
function useChosenSource(range: WebhookStatusRange, data: WebhookStatusOverview | undefined) {
  const [source, setSource] = useState(ALL);
  const names = data?.sources.map((s) => s.name) ?? [];
  const chosen = source !== ALL && names.includes(source) ? source : "";
  const one = useWebhookStatus(range, chosen, chosen !== "");
  return {
    chosen,
    setSource,
    rejections: chosen ? one.data?.rejections : data?.rejections,
    loading: chosen !== "" && one.isLoading,
  };
}

export function WebhooksPage({ onNavigate }: { onNavigate: (path: string) => void }) {
  const [range, setRange] = useState<WebhookStatusRange>("hour");
  const { data, isLoading, isError } = useWebhookStatus(range);
  const { chosen, setSource, rejections, loading } = useChosenSource(range, data);
  const open = (name: string) => onNavigate(`/admin/webhooks/${encodeURIComponent(name)}`);

  return (
    <div className="space-y-4">
      <div className="flex justify-end">
        <Button type="button" size="sm" onClick={() => onNavigate("/admin/webhooks/new")}>
          <Plus />
          New source
        </Button>
      </div>
      {isError ? (
        <Alert variant="destructive" className="py-2">
          <AlertDescription>
            The webhook sources could not be read. Sources keep receiving whether or not this page can list them.
          </AlertDescription>
        </Alert>
      ) : !isLoading && data?.sources.length === 0 ? (
        <NoSources />
      ) : (
        <>
          <VolumeSection
            data={data}
            isLoading={isLoading}
            range={range}
            onRange={setRange}
            chosen={chosen}
            onSource={setSource}
          />
          <SourcesTable sources={data?.sources} isLoading={isLoading} onOpen={open} />
          <SpoolSection spools={data?.spools} />
          <RejectionsSection
            rejections={rejections}
            isLoading={isLoading || loading}
            range={range}
            source={chosen}
            onOpen={open}
          />
        </>
      )}
    </div>
  );
}

function VolumeSection({
  data,
  isLoading,
  range,
  onRange,
  chosen,
  onSource,
}: {
  data: WebhookStatusOverview | undefined;
  isLoading: boolean;
  range: WebhookStatusRange;
  onRange: (r: WebhookStatusRange) => void;
  chosen: string;
  onSource: (s: string) => void;
}) {
  const names = data?.sources.map((s) => s.name) ?? [];
  const series = useMemo(
    () => (data ? volumeSeries(data, chosen) : { buckets: [], outcomes: [], total: 0 }),
    [data, chosen],
  );
  return (
    <SectionCard
      title="Requests"
      action={
        <div className="flex flex-wrap items-center gap-2">
          <Select value={chosen === "" ? ALL : chosen} onValueChange={onSource}>
            <SelectTrigger size="sm" className="h-7 w-44 text-xs" aria-label="Source">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={ALL}>All sources</SelectItem>
              {names.map((n) => (
                <SelectItem key={n} value={n}>
                  {n}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <div className="flex gap-1">
            {RANGES.map((r) => (
              <Button
                key={r.value}
                type="button"
                size="xs"
                variant={range === r.value ? "default" : "outline"}
                aria-pressed={range === r.value}
                onClick={() => onRange(r.value)}
              >
                {r.label}
              </Button>
            ))}
          </div>
        </div>
      }
    >
      <VolumeChart buckets={series.buckets} outcomes={series.outcomes} total={series.total} isLoading={isLoading} />
      {series.total > 0 && (
        <ul className="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground" aria-label="Totals by outcome">
          {series.outcomes.map((o) => (
            <li key={o}>
              {outcomeLabel(o)}{" "}
              <span className="font-medium tabular-nums text-foreground">
                {series.buckets.reduce((n, b) => n + (b[o] as number), 0).toLocaleString()}
              </span>
            </li>
          ))}
        </ul>
      )}
    </SectionCard>
  );
}

function SourcesTable({
  sources,
  isLoading,
  onOpen,
}: {
  sources: WebhookSourceStatus[] | undefined;
  isLoading: boolean;
  onOpen: (name: string) => void;
}) {
  return (
    <div className="rounded-md border">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Source</TableHead>
            <TableHead>Health</TableHead>
            <TableHead>Last event</TableHead>
            <TableHead className="text-right">Last hour</TableHead>
            <TableHead className="text-right">Last day</TableHead>
            <TableHead className="text-right">Response time</TableHead>
            <TableHead className="text-right">Pending</TableHead>
            <TableHead>Failing</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {isLoading ? (
            <TableRow>
              <TableCell colSpan={8} className="py-6 text-center text-sm text-muted-foreground">
                Loading...
              </TableCell>
            </TableRow>
          ) : (
            (sources ?? []).map((s) => (
              <TableRow key={s.name} className="cursor-pointer" onClick={() => onOpen(s.name)}>
                <TableCell>
                  <div className="font-medium">{s.name}</div>
                  <div className="font-mono text-xs text-muted-foreground">{s.table}</div>
                </TableCell>
                <TableCell>
                  <Badge variant={HEALTH_VARIANTS[s.health]} title={HEALTH_HINTS[s.health]}>
                    {HEALTH_LABELS[s.health]}
                  </Badge>
                </TableCell>
                <TableCell className="whitespace-nowrap text-sm">
                  {s.last_event_at ? (
                    <span title={new Date(s.last_event_at).toLocaleString()}>{relativeTime(s.last_event_at)}</span>
                  ) : (
                    <span className="text-muted-foreground">Never</span>
                  )}
                </TableCell>
                <CountsCell counts={s.last_hour} />
                <CountsCell counts={s.last_day} />
                <ResponseTimeCell source={s} />
                <TableCell className="text-right tabular-nums">{s.pending}</TableCell>
                <TableCell className="max-w-72">
                  {s.failing > 0 ? (
                    <>
                      <span className="font-medium tabular-nums text-red-700 dark:text-red-300">{s.failing}</span>
                      {s.last_error && (
                        <div className="truncate font-mono text-xs text-muted-foreground" title={s.last_error}>
                          {s.last_error}
                        </div>
                      )}
                    </>
                  ) : (
                    <span className="tabular-nums">0</span>
                  )}
                </TableCell>
              </TableRow>
            ))
          )}
        </TableBody>
      </Table>
    </div>
  );
}

/** CountsCell is a period's accepted requests, and its rejected ones beneath
 * when there were any. */
function CountsCell({ counts }: { counts: Record<string, number> }) {
  const { accepted, rejected } = splitCounts(counts);
  return (
    <TableCell className="text-right tabular-nums">
      <div>{accepted.toLocaleString()} accepted</div>
      {rejected > 0 && <div className="text-xs text-red-700 dark:text-red-300">{rejected.toLocaleString()} rejected</div>}
    </TableCell>
  );
}

/** ResponseTimeCell is how long the last hour's accepted requests waited for
 * their answer: the 99th percentile, and the slowest beneath it. A source with
 * none in the hour falls back to the last day. */
function ResponseTimeCell({ source }: { source: WebhookSourceStatus }) {
  const hour = source.ack_last_hour;
  const span = hour && hour.count > 0 ? hour : source.ack_last_day;
  if (!span || span.count === 0) {
    return <TableCell className="text-right text-muted-foreground">-</TableCell>;
  }
  return (
    <TableCell className="text-right tabular-nums" title="99th percentile, and the slowest request beneath it">
      <div>p99 {formatMs(span.p99_ms)}</div>
      <div className="text-xs text-muted-foreground">
        max {formatMs(span.max_ms)}
        {span === hour ? "" : " (24 hours)"}
      </div>
    </TableCell>
  );
}

function RejectionsSection({
  rejections,
  isLoading,
  range,
  source,
  onOpen,
}: {
  rejections: WebhookSourceRejection[] | undefined;
  isLoading: boolean;
  range: WebhookStatusRange;
  source: string;
  onOpen: (name: string) => void;
}) {
  if (isLoading) return null;
  const when = range === "hour" ? "in the last hour" : "in the last 24 hours";
  return (
    <SectionCard title="Recent rejections">
      {!rejections || rejections.length === 0 ? (
        <p className="text-xs text-muted-foreground">No request has been rejected {when}.</p>
      ) : (
        <>
          <p className="mb-2 text-xs text-muted-foreground">
            The newest 50 of each outcome {source ? `for ${source}` : "across every source"} {when}. A row counts
            the requests rejected for the same reason within a minute of each other. A request body is never kept.
          </p>
          <RejectionsTable rows={rejections} showSource onOpen={(r) => onOpen(r.source)} />
        </>
      )}
    </SectionCard>
  );
}

/** SpoolSection is every receiver's local spool (#2121): what each replica
 * acknowledged and has not yet drained to object storage. It is shown only
 * where a receiver spools. */
function SpoolSection({ spools }: { spools: WebhookReplicaSpool[] | undefined }) {
  if (!spools || spools.length === 0) return null;
  return (
    <SectionCard title="Spool">
      <p className="mb-2 text-xs text-muted-foreground">
        Each receiver with a spool answers a request once its events are on its own disk, and drains them to
        object storage after. Events waiting here are acknowledged and not yet in a table. A receiver that is not
        reporting has stopped, and drains what it holds when it starts again.
      </p>
      <div className="rounded-md border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Replica</TableHead>
              <TableHead className="text-right">Waiting</TableHead>
              <TableHead className="text-right">Size</TableHead>
              <TableHead>Oldest waiting</TableHead>
              <TableHead className="text-right">Drained</TableHead>
              <TableHead className="text-right">Drain failures</TableHead>
              <TableHead>Last error</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {spools.map((s) => (
              <TableRow key={s.replica}>
                <TableCell>
                  <div className="flex items-center gap-2">
                    <span className="font-mono text-xs" title={s.dir}>
                      {s.replica}
                    </span>
                    <Badge
                      variant={s.reporting ? "success" : "warning"}
                      title={s.reporting ? `Last reported ${relativeTime(s.updated_at)}` : `No report since ${new Date(s.updated_at).toLocaleString()}`}
                    >
                      {s.reporting ? "Reporting" : "Not reporting"}
                    </Badge>
                  </div>
                </TableCell>
                <TableCell className="text-right tabular-nums">{s.events.toLocaleString()}</TableCell>
                <TableCell className="text-right tabular-nums" title={`Bound: ${formatBytes(s.max_bytes)}`}>
                  {formatBytes(s.bytes)}
                </TableCell>
                <TableCell className="text-xs">
                  {s.oldest_at ? (
                    <span title={`${new Date(s.oldest_at).toLocaleString()}; bound: ${formatMs(s.max_age_seconds * 1000)}`}>
                      {relativeTime(s.oldest_at)}
                    </span>
                  ) : (
                    <span className="text-muted-foreground">None</span>
                  )}
                </TableCell>
                <TableCell className="text-right tabular-nums">{s.drained_events.toLocaleString()}</TableCell>
                <TableCell className="text-right tabular-nums">{s.drain_failures.toLocaleString()}</TableCell>
                <TableCell className="max-w-xs truncate text-xs text-muted-foreground" title={s.last_error}>
                  {s.last_error || "None"}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
    </SectionCard>
  );
}

function NoSources() {
  return (
    <EmptyState icon={Webhook} className="py-10">
      <p className="font-medium text-foreground">No webhook source yet.</p>
      <p className="mx-auto mt-1.5 max-w-lg">
        A source is an address an external system posts events to, such as an email service reporting
        deliveries or a CRM reporting changed contacts. Each event is stored as it arrives, can be queried as a
        table straight away, and is compacted into one Parquet file per window, an hour unless the source sets a shorter one.
      </p>
    </EmptyState>
  );
}
