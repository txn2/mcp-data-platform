import { useMemo, useState } from "react";
import { Plus, Webhook } from "lucide-react";
import { useWebhookStatus } from "@/api/admin/hooks";
import type {
  WebhookSourceRejection,
  WebhookSourceStatus,
  WebhookStatusOverview,
  WebhookStatusRange,
} from "@/api/admin/types";
import { EmptyState } from "@/components/patterns/EmptyState";
import { SectionCard } from "@/components/patterns/SectionCard";
import { relativeTime } from "@/components/provenance/parts";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { VolumeChart } from "./VolumeChart";
import {
  HEALTH_HINTS,
  HEALTH_LABELS,
  HEALTH_VARIANTS,
  outcomeLabel,
  splitCounts,
  volumeSeries,
} from "./webhookOverview";

// WebhooksPage is the overview of every inbound webhook source (#1870, #1979),
// read in one request: the request volume by outcome, each source's health
// and counts, and the newest rejected requests across all of them. A row
// opens the source's page, which is where its address and settings are.

const RANGES: { value: WebhookStatusRange; label: string }[] = [
  { value: "hour", label: "Last hour" },
  { value: "day", label: "Last 24 hours" },
];

// ALL is the source picker's value for every source; a Radix select item
// cannot carry an empty value.
const ALL = "__all__";

export function WebhooksPage({ onNavigate }: { onNavigate: (path: string) => void }) {
  const [range, setRange] = useState<WebhookStatusRange>("hour");
  const { data, isLoading, isError } = useWebhookStatus(range);
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
          <VolumeSection data={data} isLoading={isLoading} range={range} onRange={setRange} />
          <SourcesTable sources={data?.sources} isLoading={isLoading} onOpen={open} />
          <RejectionsSection rejections={data?.rejections} isLoading={isLoading} onOpen={open} />
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
}: {
  data: WebhookStatusOverview | undefined;
  isLoading: boolean;
  range: WebhookStatusRange;
  onRange: (r: WebhookStatusRange) => void;
}) {
  const [source, setSource] = useState(ALL);
  const names = data?.sources.map((s) => s.name) ?? [];
  // A source deleted while chosen falls back to every source.
  const chosen = source !== ALL && names.includes(source) ? source : "";
  const series = useMemo(
    () => (data ? volumeSeries(data, chosen) : { buckets: [], outcomes: [], total: 0 }),
    [data, chosen],
  );
  return (
    <SectionCard
      title="Requests"
      action={
        <div className="flex flex-wrap items-center gap-2">
          <Select value={chosen === "" ? ALL : chosen} onValueChange={setSource}>
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
            <TableHead className="text-right">Pending</TableHead>
            <TableHead>Failing</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {isLoading ? (
            <TableRow>
              <TableCell colSpan={7} className="py-6 text-center text-sm text-muted-foreground">
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

function RejectionsSection({
  rejections,
  isLoading,
  onOpen,
}: {
  rejections: WebhookSourceRejection[] | undefined;
  isLoading: boolean;
  onOpen: (name: string) => void;
}) {
  if (isLoading) return null;
  return (
    <SectionCard title="Recent rejections">
      {!rejections || rejections.length === 0 ? (
        <p className="text-xs text-muted-foreground">No request has been rejected.</p>
      ) : (
        <>
          <p className="mb-2 text-xs text-muted-foreground">
            The last 50 across every source. A request body is never kept.
          </p>
          <div className="rounded-md border">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Source</TableHead>
                  <TableHead>When</TableHead>
                  <TableHead>Outcome</TableHead>
                  <TableHead>Reason</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {rejections.map((r, i) => (
                  <TableRow key={`${r.source}-${r.at}-${i}`} className="cursor-pointer" onClick={() => onOpen(r.source)}>
                    <TableCell className="text-xs font-medium">{r.source}</TableCell>
                    <TableCell className="whitespace-nowrap text-xs">{new Date(r.at).toLocaleString()}</TableCell>
                    <TableCell className="text-xs">{outcomeLabel(r.outcome)}</TableCell>
                    <TableCell className="text-xs">{r.reason}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        </>
      )}
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
