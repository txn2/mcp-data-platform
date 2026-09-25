import { useState } from "react";
import { AlertTriangle, Pencil, Trash2, Webhook } from "lucide-react";
import { useDeleteWebhookSource, useWebhookSource } from "@/api/admin/hooks";
import type { WebhookSource, WebhookStatus } from "@/api/admin/types";
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

// WebhookDetailPage is one source (#1870): the address to give the sender,
// whether requests are arriving and being accepted, how far compaction and
// retention have got, and why recent requests were refused.

// OUTCOMES orders the request outcomes the platform counts, accepted first.
const OUTCOMES: { key: string; label: string }[] = [
  { key: "accepted", label: "Accepted" },
  { key: "unauthorized", label: "Unauthorized" },
  { key: "too_large", label: "Too large" },
  { key: "rate_limited", label: "Rate limited" },
  { key: "buffer_full", label: "Buffer full" },
  { key: "write_failed", label: "Write failed" },
  { key: "invalid_body", label: "Invalid body" },
];

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

/** ModeFacts are the settings the sender has to match for its mode. */
function ModeFacts({ auth: a }: { auth: WebhookSource["auth"] }) {
  switch (a.mode) {
    case "hmac":
      return (
        <>
          <Fact label="Signature header" value={`${a.signature_header ?? ""}${a.prefix ? ` (prefix ${a.prefix})` : ""}`} mono />
          <Fact label="Signature" value={`HMAC-${(a.algorithm ?? "sha256").toUpperCase()}, ${a.encoding ?? "hex"}`} />
          <Fact label="Signed" value={signedText(a)} />
        </>
      );
    case "header_token":
      return <Fact label="Token header" value={a.header ?? ""} mono />;
    case "basic":
      return <Fact label="Username" value={a.username ?? ""} mono />;
    default:
      return null;
  }
}

/** signedText says what an HMAC signature is computed over. */
function signedText(a: WebhookSource["auth"]): string {
  if (!a.timestamp_header) return "the body";
  const what = a.signed === "timestamp.body" ? "timestamp.body" : "body";
  return `${what}; timestamp in ${a.timestamp_header}, ${a.tolerance_seconds ?? 300}s tolerance`;
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
        <Fact label="Raw events kept after compaction" value={`${c.raw_retention_days ?? 7} days`} />
        <Fact label="Compacted files kept" value={keep} />
        <Fact label="Compacted files visible to" value={c.persona ? `The ${c.persona} persona` : "Administrators only"} />
      </dl>
      <p className="mt-3 text-xs text-muted-foreground">
        Each window is compacted shortly after it ends. Until then its events are read straight from what
        arrived, duplicates included.
      </p>
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
          <p className="mb-2 text-xs text-muted-foreground">The last 50. A request body is never kept.</p>
          <div className="rounded-md border">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>When</TableHead>
                  <TableHead>Outcome</TableHead>
                  <TableHead>Reason</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {status.rejections.map((r, i) => (
                  <TableRow key={`${r.at}-${i}`}>
                    <TableCell className="whitespace-nowrap text-xs">{new Date(r.at).toLocaleString()}</TableCell>
                    <TableCell className="text-xs">{OUTCOMES.find((o) => o.key === r.outcome)?.label ?? r.outcome}</TableCell>
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
