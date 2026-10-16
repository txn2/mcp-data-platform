import { INDEX_POLICY_LABELS } from "@/pages/resources/parts/IndexPolicyField";
import { useState } from "react";
import { AlertTriangle, Pencil, RefreshCw, Trash2, Webhook } from "lucide-react";
import { useDeleteWebhookSource, useSyncWebhookPartitions, useWebhookSource } from "@/api/admin/hooks";
import type { WebhookAckLatency, WebhookSource, WebhookStatus } from "@/api/admin/types";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { LoadingIndicator } from "@/components/LoadingIndicator";
import { PageHeader } from "@/components/patterns/PageHeader";
import { SectionCard } from "@/components/patterns/SectionCard";
import { CopyButton, relativeTime } from "@/components/provenance/parts";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { MODE_LABELS, senderURL, windowLength } from "./webhookForm";
import { RejectionsTable } from "./RejectionsTable";
import { OUTCOMES, formatMs } from "./webhookOverview";

// WebhookDetailPage is one source (#1870): the address to give the sender,
// whether requests are arriving and being accepted, how long accepted
// requests waited for their answer, how far compaction and retention have
// got, and why recent requests were refused.

export function WebhookDetailPage({
  name,
  onBack,
  onNavigate,
}: {
  name: string;
  onBack: () => void;
  onNavigate: (path: string) => void;
}) {
  const { data, isLoading, error } = useWebhookSource(name);

  if (isLoading) {
    return (
      <div className="flex justify-center py-16">
        <LoadingIndicator />
      </div>
    );
  }
  if (error || !data) {
    return (
      <div className="space-y-4">
        <PageHeader backLabel="Webhooks" onBack={onBack} title={name} />
        <Alert variant="destructive" className="py-2">
          <AlertDescription>There is no webhook source named {name}, or it could not be read.</AlertDescription>
        </Alert>
      </div>
    );
  }
  const { source, status } = data;
  return (
    <div className="space-y-4">
      <PageHeader
        backLabel="Webhooks"
        onBack={onBack}
        icon={Webhook}
        title={source.name}
        urn={source.table}
        subtitle={
          <>
            {source.enabled ? "Enabled" : "Disabled"} &middot; {MODE_LABELS[source.auth.mode] ?? source.auth.mode}{" "}
            &middot; table on <span className="font-medium text-foreground">{source.connection}</span>
          </>
        }
        actions={
          <>
            <Button
              type="button"
              variant="outline"
              size="xs"
              onClick={() => onNavigate(`/admin/webhooks/${encodeURIComponent(source.name)}/edit`)}
            >
              <Pencil />
              Edit
            </Button>
            <DeleteAction name={source.name} onDone={onBack} />
          </>
        }
      />

      {!source.enabled && (
        <Alert className="py-2">
          <AlertDescription>
            This source is disabled: requests to it are answered 404 and nothing is stored.
          </AlertDescription>
        </Alert>
      )}
      {status.failing > 0 && (
        <Alert variant="destructive" className="py-2">
          <AlertTriangle />
          <AlertDescription>
            {status.failing} {status.failing === 1 ? "window has" : "windows have"} failed to compact and will be tried
            again. Their events are still queryable from the raw segments.
            {status.last_error ? <span className="mt-1 block font-mono text-xs">{status.last_error}</span> : null}
          </AlertDescription>
        </Alert>
      )}

      <SenderSection source={source} />
      <ActivitySection status={status} />
      <ResponseTimeSection status={status} />
      <StorageSection source={source} status={status} />
      <RejectionsSection status={status} />
    </div>
  );
}

function SenderSection({ source }: { source: WebhookSource }) {
  const url = senderURL(window.location.origin, source);
  const a = source.auth;
  return (
    <SectionCard title="Give this to the sender">
      <div className="flex items-start gap-2">
        <pre className="min-w-0 flex-1 overflow-x-auto rounded-md bg-muted p-3 font-mono text-xs">{url}</pre>
        <CopyButton text={url} label="Copy the address" />
      </div>
      <dl className="mt-3 grid gap-3 text-xs sm:grid-cols-3">
        <ModeFacts auth={a} />
        <Fact label="Secret" value={a.secret_set ? "Set" : "Not set"} />
        {a.previous_secret_until && (
          <Fact label="Previous secret accepted until" value={new Date(a.previous_secret_until).toLocaleString()} />
        )}
        <Fact label="CloudEvents handshake" value={source.config.handshake === "cloudevents" ? "Answered" : "Not answered"} />
      </dl>
    </SectionCard>
  );
}

/** HMACFacts are the signing settings an HMAC sender has to match. */
function HMACFacts({ auth: a }: { auth: WebhookSource["auth"] }) {
  return (
    <>
      <Fact label="Signature header" value={`${a.signature_header ?? ""}${a.prefix ? ` (prefix ${a.prefix})` : ""}`} mono />
      <Fact label="Signature" value={`HMAC-${(a.algorithm ?? "sha256").toUpperCase()}, ${a.encoding ?? "hex"}`} />
      <Fact label="Signed content" value={signedText(a)} />
      {a.id_header && <Fact label="ID header" value={a.id_header} mono />}
    </>
  );
}

/** ModeFacts are the settings the sender has to match for its mode. */
function ModeFacts({ auth: a }: { auth: WebhookSource["auth"] }) {
  switch (a.mode) {
    case "hmac":
      return <HMACFacts auth={a} />;
    case "header_token":
      return <Fact label="Token header" value={a.header ?? ""} mono />;
    case "basic":
      return <Fact label="Username" value={a.username ?? ""} mono />;
    default:
      return null;
  }
}

/** SIGNED_LABELS name what a signature covers, as the editor names it. */
const SIGNED_BODY = "Body";
const SIGNED_LABELS: Record<string, string> = {
  body: SIGNED_BODY,
  "timestamp.body": "Timestamp and body",
  "id.timestamp.body": "ID, timestamp and body",
};

/** signedText says what an HMAC signature is computed over. */
export function signedText(a: WebhookSource["auth"]): string {
  const tolerance = `${a.tolerance_seconds ?? 300}s tolerance`;
  if (a.header_format === "stripe") return `Timestamp and body; Stripe header format, ${tolerance}`;
  if (!a.timestamp_header) return SIGNED_BODY;
  const what = SIGNED_LABELS[a.signed ?? "body"] ?? SIGNED_BODY;
  return `${what}; timestamp in ${a.timestamp_header}, ${tolerance}`;
}

function ActivitySection({ status }: { status: WebhookStatus }) {
  const seen = OUTCOMES.filter((o) => (status.last_day[o.key] ?? 0) > 0);
  return (
    <SectionCard title="Requests">
      <p className="text-sm">
        {status.last_segment_at ? (
          <>
            Last event received <span className="font-medium">{relativeTime(status.last_segment_at)}</span>.
          </>
        ) : (
          <>No event has been received yet.</>
        )}{" "}
        <span className="text-muted-foreground">
          A sender that stops produces no error here; this is how a quiet source shows.
        </span>
      </p>
      {seen.length === 0 ? (
        <p className="mt-2 text-xs text-muted-foreground">No requests in the last day.</p>
      ) : (
        <div className="mt-3 rounded-md border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Outcome</TableHead>
                <TableHead className="text-right">Last hour</TableHead>
                <TableHead className="text-right">Last day</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {seen.map((o) => (
                <TableRow key={o.key}>
                  <TableCell>
                    {o.key === "accepted" ? o.label : <Badge variant="danger">{o.label}</Badge>}
                  </TableCell>
                  <TableCell className="text-right tabular-nums">{status.last_hour[o.key] ?? 0}</TableCell>
                  <TableCell className="text-right tabular-nums">{status.last_day[o.key] ?? 0}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
    </SectionCard>
  );
}

const RESPONSE_SPANS: { key: "ack_last_hour" | "ack_last_day"; label: string }[] = [
  { key: "ack_last_hour", label: "Last hour" },
  { key: "ack_last_day", label: "Last 24 hours" },
];

/** ResponseTimeSection is how long requests answered 202 waited for that
 * answer (#2107), and the slowest with where its time went, so a response
 * time creeping toward a sender's timeout shows before requests fail. */
function ResponseTimeSection({ status }: { status: WebhookStatus }) {
  const day = status.ack_last_day;
  const slowest = day?.slowest;
  return (
    <SectionCard title="Response time">
      {!day || day.count === 0 ? (
        <p className="text-xs text-muted-foreground">No request was accepted in the last 24 hours.</p>
      ) : (
        <>
          <div className="rounded-md border">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Period</TableHead>
                  <TableHead className="text-right">Requests</TableHead>
                  <TableHead className="text-right">Median</TableHead>
                  <TableHead className="text-right">99th percentile</TableHead>
                  <TableHead className="text-right">Slowest</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {RESPONSE_SPANS.map((span) => (
                  <ResponseTimeRow key={span.key} label={span.label} latency={status[span.key]} />
                ))}
              </TableBody>
            </Table>
          </div>
          {slowest && (
            <div className="mt-3">
              <h4 className="text-xs font-medium">Slowest request</h4>
              <dl className="mt-1 grid gap-3 text-xs sm:grid-cols-5">
                <Fact label="When" value={new Date(slowest.at).toLocaleString()} />
                <Fact label="Total" value={formatMs(slowest.total_ms)} />
                <Fact label="Buffer" value={formatMs(slowest.buffer_ms)} />
                <Fact label="Recording" value={formatMs(slowest.mark_ms)} />
                <Fact label="Writing" value={formatMs(slowest.put_ms)} />
              </dl>
            </div>
          )}
          <p className="mt-3 text-xs text-muted-foreground">
            The time from a request arriving to its answer. Expect the source's flush interval (one second unless
            it sets another) plus one write to object storage, or one write to the replica's own disk where its
            receiver has a spool. Median and 99th percentile are at most the value shown. Buffer is the wait for the
            flush, Recording the database writes that record the window, and Writing the write the answer waited
            for: to object storage, or to the replica's spool.
          </p>
        </>
      )}
    </SectionCard>
  );
}

function ResponseTimeRow({ label, latency }: { label: string; latency: WebhookAckLatency | undefined }) {
  const none = !latency || latency.count === 0;
  return (
    <TableRow>
      <TableCell>{label}</TableCell>
      <TableCell className="text-right tabular-nums">{none ? 0 : latency.count.toLocaleString()}</TableCell>
      <TableCell className="text-right tabular-nums">{none ? "-" : formatMs(latency.p50_ms)}</TableCell>
      <TableCell className="text-right tabular-nums">{none ? "-" : formatMs(latency.p99_ms)}</TableCell>
      <TableCell className="text-right tabular-nums">{none ? "-" : formatMs(latency.max_ms)}</TableCell>
    </TableRow>
  );
}

function StorageSection({ source, status }: { source: WebhookSource; status: WebhookStatus }) {
  const c = source.config;
  const keep = c.compacted_retention_days === 0 ? "forever" : `${c.compacted_retention_days ?? 400} days`;
  return (
    <SectionCard title="Compaction and retention">
      <dl className="grid gap-3 text-xs sm:grid-cols-3">
        <Fact label="Compacted every" value={windowLength(c.compact_every_minutes ?? 60)} />
        <Fact label="Last compacted window" value={status.last_compacted_window ? windowLabel(status.last_compacted_window) : "None yet"} />
        <Fact label="Windows waiting to be compacted" value={String(status.pending)} />
        <Fact label="Oldest window held" value={status.oldest_window ? windowLabel(status.oldest_window) : "None"} />
        <Fact label="Raw events kept after compaction" value={`${c.raw_retention_days ?? 1} days`} />
        <Fact label="Compacted files kept" value={keep} />
        <Fact label="Compacted files visible to" value={c.persona ? `The ${c.persona} persona` : "Administrators only"} />
        <Fact label="Search" value={c.index ? INDEX_POLICY_LABELS[c.index] : "Default"} />
        <Fact label="Raw files in the current window" value={String(status.segments_open_window)} />
        <Fact
          label="Raw files per window, 95th percentile (day)"
          value={String(status.segments_per_window_p95)}
        />
      </dl>
      <p className="mt-3 text-xs text-muted-foreground">
        Each window is compacted shortly after it ends. Until then its events are read straight from what
        arrived, duplicates included, and its raw files are merged every few minutes so a reader reads a
        handful of files rather than one per write.
      </p>
      <ResyncAction name={source.name} />
    </SectionCard>
  );
}

function RejectionsSection({ status }: { status: WebhookStatus }) {
  return (
    <SectionCard title="Rejected requests">
      {status.rejections.length === 0 ? (
        <p className="text-xs text-muted-foreground">No request has been rejected.</p>
      ) : (
        <>
          <p className="mb-2 text-xs text-muted-foreground">
            The newest 50 of each outcome. A row counts the requests rejected for the same reason within a
            minute of each other. A request body is never kept.
          </p>
          <RejectionsTable rows={status.rejections} />
        </>
      )}
    </SectionCard>
  );
}

/** ResyncAction runs the full listing of a source's raw files that retention
 * never runs (#2133), behind a confirm that names its cost. */
function ResyncAction({ name }: { name: string }) {
  const [open, setOpen] = useState(false);
  const [done, setDone] = useState(false);
  const sync = useSyncWebhookPartitions();
  return (
    <div className="mt-3 flex items-center gap-3">
      <Button type="button" variant="outline" size="xs" onClick={() => setOpen(true)}>
        <RefreshCw />
        Resync partitions
      </Button>
      {done && <span className="text-xs text-muted-foreground">Partitions resynced.</span>}
      <ConfirmDialog
        open={open}
        onOpenChange={setOpen}
        title={`Resync the partitions of ${name}?`}
        description="Lists every raw file the source holds and makes the raw table's partitions match. Retention keeps them by name; this is for when the two have drifted, after a restore for example. On a busy source it can take minutes and slows the object store while it runs."
        confirmLabel="Resync partitions"
        loading={sync.isPending}
        error={sync.error ? sync.error.message : undefined}
        onConfirm={() =>
          sync.mutate(name, {
            onSuccess: () => {
              setOpen(false);
              setDone(true);
            },
          })
        }
      />
    </div>
  );
}

function DeleteAction({ name, onDone }: { name: string; onDone: () => void }) {
  const [open, setOpen] = useState(false);
  const del = useDeleteWebhookSource();
  return (
    <>
      <Button type="button" variant="outline" size="xs" onClick={() => setOpen(true)}>
        <Trash2 />
        Delete
      </Button>
      <ConfirmDialog
        open={open}
        onOpenChange={setOpen}
        title={`Delete ${name}?`}
        description="The source stops receiving, and everything it stored is deleted: its table, every compacted hour, and every raw event."
        confirmLabel="Delete source"
        destructive
        loading={del.isPending}
        error={del.error ? del.error.message : undefined}
        onConfirm={() => del.mutate(name, { onSuccess: onDone })}
      />
    </>
  );
}

function Fact({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div>
      <dt className="text-muted-foreground">{label}</dt>
      <dd className={mono ? "mt-0.5 font-mono break-all" : "mt-0.5"}>{value || "-"}</dd>
    </div>
  );
}

/** windowLabel renders a window by the UTC minute it starts at. */
function windowLabel(iso: string): string {
  const d = new Date(iso).toISOString();
  return `${d.slice(0, 10)} ${d.slice(11, 16)} UTC`;
}
