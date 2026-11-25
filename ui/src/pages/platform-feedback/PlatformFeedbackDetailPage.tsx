import { useEffect, useState } from "react";
import { MessageSquareWarning } from "lucide-react";
import {
  PLATFORM_FEEDBACK_STATUSES,
  usePlatformFeedbackPost,
  useResendPlatformFeedback,
  useSetPlatformFeedbackStatus,
  type PlatformFeedbackDelivery,
  type PlatformFeedbackDetail,
  type PlatformFeedbackStatus,
} from "@/api/admin/hooks";
import { PageHeader } from "@/components/patterns/PageHeader";
import { SectionCard } from "@/components/patterns/SectionCard";
import { relativeTime } from "@/components/provenance/parts";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  STATUS_LABELS,
  deliveryLabel,
  statusLabel,
  statusVariant,
} from "./platformFeedbackFormat";

// PlatformFeedbackDetailPage is one post (#2820): what the agent was trying to
// do and what blocked it, each report counted on it, and where it was sent.
export function PlatformFeedbackDetailPage({
  id,
  onBack,
}: {
  id: string;
  onBack: () => void;
}) {
  const { data, isLoading, error } = usePlatformFeedbackPost(id);
  const setStatus = useSetPlatformFeedbackStatus(id);
  const [status, setDraft] = useState<PlatformFeedbackStatus>("open");
  useEffect(() => {
    if (data) setDraft(data.status);
  }, [data]);

  if (isLoading)
    return <p className="text-sm text-muted-foreground">Loading...</p>;
  if (error || !data) {
    return (
      <div className="space-y-4">
        <PageHeader
          backLabel="Platform feedback"
          onBack={onBack}
          title="Platform feedback"
        />
        <Alert variant="destructive">
          <AlertDescription>
            {error?.message ?? "No such platform feedback post."}
          </AlertDescription>
        </Alert>
      </div>
    );
  }
  return (
    <div className="space-y-4">
      <PageHeader
        backLabel="Platform feedback"
        onBack={onBack}
        icon={MessageSquareWarning}
        title={data.trying_to}
        subtitle={<Subtitle post={data} />}
        actions={
          <div className="flex items-center gap-2">
            <label
              htmlFor="platform-feedback-status"
              className="text-xs text-muted-foreground"
            >
              Status
            </label>
            <select
              id="platform-feedback-status"
              className="h-9 rounded-md border bg-background px-2 text-sm"
              value={status}
              onChange={(e) =>
                setDraft(e.target.value as PlatformFeedbackStatus)
              }
            >
              {PLATFORM_FEEDBACK_STATUSES.map((s) => (
                <option key={s} value={s}>
                  {STATUS_LABELS[s]}
                </option>
              ))}
            </select>
            <Button
              size="sm"
              disabled={status === data.status || setStatus.isPending}
              onClick={() => setStatus.mutate(status)}
            >
              Save
            </Button>
          </div>
        }
      />
      {setStatus.error && (
        <Alert variant="destructive">
          <AlertDescription>{setStatus.error.message}</AlertDescription>
        </Alert>
      )}
      <div className="grid gap-4 lg:grid-cols-[1fr_22rem]">
        <SectionCard title="Post">
          <dl className="grid gap-4 sm:grid-cols-2">
            <Field label="Trying to">{data.trying_to}</Field>
            <Field label="Tool">
              {data.tool ? (
                <span className="font-mono text-sm">{data.tool}</span>
              ) : (
                "None"
              )}
            </Field>
            <Field label="Blocked by" wide>
              {data.blocked_by}
            </Field>
            {data.suggestion && (
              <Field label="Suggestion" wide>
                {data.suggestion}
              </Field>
            )}
          </dl>
          <Reports post={data} />
        </SectionCard>
        <SectionCard title="Deliveries">
          <Deliveries post={data} />
        </SectionCard>
      </div>
    </div>
  );
}

function Subtitle({ post }: { post: PlatformFeedbackDetail }) {
  const first =
    post.reports.length > 0
      ? post.reports[post.reports.length - 1]!.reported_at
      : post.created_at;
  return (
    <span className="flex flex-wrap items-center gap-2">
      <Badge variant={statusVariant(post.status)}>
        {statusLabel(post.status)}
      </Badge>
      <span>
        Reported {post.report_count} time{post.report_count === 1 ? "" : "s"},
        first {relativeTime(first)}
      </span>
    </span>
  );
}

function Reports({ post: data }: { post: PlatformFeedbackDetail }) {
  return (
    <div className="mt-5 space-y-2">
      <h3 className="text-sm font-semibold">Reports</h3>
      <div className="overflow-x-auto">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>When</TableHead>
              <TableHead>Persona</TableHead>
              <TableHead>Calls involved</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {data.reports.map((r, i) => (
              <TableRow key={`${r.reported_at}-${i}`}>
                <TableCell className="text-xs text-muted-foreground">
                  {relativeTime(r.reported_at)}
                </TableCell>
                <TableCell>{r.persona || "Unknown"}</TableCell>
                <TableCell className="font-mono text-xs">
                  {(r.calls ?? []).length > 0 ? (
                    (r.calls ?? []).join(", ")
                  ) : (
                    <span className="font-sans text-muted-foreground">
                      None
                    </span>
                  )}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
      <p className="text-xs text-muted-foreground">
        Showing {data.reports.length} of {data.report_count}. Each later report
        was close enough to this post to be counted on it.
      </p>
    </div>
  );
}

function Deliveries({ post: data }: { post: PlatformFeedbackDetail }) {
  const id = data.id;
  return (
    <div className="space-y-3">
      {data.deliveries.length === 0 ? (
        <p className="text-sm text-muted-foreground">
          Kept here only: no channel was chosen when it was posted.
        </p>
      ) : (
        data.deliveries.map((d, i) => (
          <Delivery key={`${d.channel}-${i}`} postId={id} delivery={d} />
        ))
      )}
      <p className="text-xs text-muted-foreground">
        A repeat is counted here and is not sent again. A status change is not
        sent.
      </p>
    </div>
  );
}

function Field({
  label,
  wide,
  children,
}: {
  label: string;
  wide?: boolean;
  children: React.ReactNode;
}) {
  return (
    <div className={wide ? "sm:col-span-2" : undefined}>
      <dt className="text-xs font-medium text-muted-foreground">{label}</dt>
      <dd className="mt-1 text-sm">{children}</dd>
    </div>
  );
}

function Delivery({
  postId,
  delivery: d,
}: {
  postId: string;
  delivery: PlatformFeedbackDelivery;
}) {
  const resend = useResendPlatformFeedback(postId);
  const b = deliveryLabel(d.status);
  const failed = d.status === "failed";
  return (
    <div
      className={`space-y-1 rounded-md border p-3 ${failed ? "border-red-300" : ""}`}
    >
      <div className="flex items-center justify-between gap-2">
        <span className="text-sm font-medium">{d.channel}</span>
        <Badge variant={b.variant}>{b.label}</Badge>
      </div>
      <p className="text-xs text-muted-foreground">
        {d.sent_at ? `Sent ${relativeTime(d.sent_at)}, ` : ""}
        {d.attempts} attempt{d.attempts === 1 ? "" : "s"}.
        {d.last_error ? ` The last answer: ${d.last_error}` : ""}
      </p>
      {failed && (
        <Button
          size="sm"
          variant="outline"
          disabled={resend.isPending || resend.isSuccess}
          onClick={() => resend.mutate(d.channel)}
        >
          {resend.isSuccess ? "Queued" : "Send again"}
        </Button>
      )}
      {resend.error && (
        <p className="text-xs text-destructive">{resend.error.message}</p>
      )}
    </div>
  );
}
