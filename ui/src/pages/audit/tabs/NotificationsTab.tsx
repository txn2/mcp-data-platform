import { useState, useMemo } from "react";
import { useNotifications, useNotificationStats } from "@/api/admin/hooks";
import type { NotificationRow, NotificationStats, NotificationStatus } from "@/api/admin/hooks";
import { StatusBadge } from "@/components/cards/StatusBadge";
import { useDebounced } from "@/lib/useDebounced";
import { AlertCircle, X } from "lucide-react";

const PER_PAGE = 20;

const STATUS_VARIANT: Record<NotificationStatus, "success" | "error" | "warning" | "neutral"> = {
  sent: "success",
  failed: "error",
  sending: "warning",
  pending: "neutral",
};

// STAT_TILES is the at-a-glance health read, ordered by what an admin looks
// for first: what broke, then what is still in flight, then what worked.
const STAT_TILES: { key: NotificationStatus; label: string; help: string }[] = [
  { key: "failed", label: "Failed", help: "Delivery attempts exhausted; these emails were never sent" },
  { key: "pending", label: "Pending", help: "Queued and waiting for the send worker" },
  { key: "sending", label: "Sending", help: "Claimed by a worker and in flight" },
  { key: "sent", label: "Sent", help: "Handed to the mail server" },
];

const CATEGORIES = [
  { value: "share", label: "Shares" },
  { value: "comment", label: "Comments" },
  { value: "mention", label: "Mentions" },
];

/**
 * NotificationsTab is the admin read on email delivery: per-status counts,
 * a filterable list of recent queue rows, and the failure detail behind a
 * bounced send.
 *
 * The queue purges resolved rows on a retention schedule, so this is recent
 * history rather than an archive. The window is stated in the header instead
 * of being left for an admin to discover by finding nothing.
 */
export function NotificationsTab() {
  const [page, setPage] = useState(1);
  const [status, setStatus] = useState("");
  const [category, setCategory] = useState("");
  const [recipientInput, setRecipientInput] = useState("");
  const [selected, setSelected] = useState<NotificationRow | null>(null);
  const recipient = useDebounced(recipientInput.trim(), 300);

  const query = useMemo(
    () => ({
      page,
      per_page: PER_PAGE,
      status: status || undefined,
      category: category || undefined,
      recipient: recipient || undefined,
    }),
    [page, status, category, recipient],
  );

  const { data, isLoading, error } = useNotifications(query);
  const { data: stats } = useNotificationStats(query);
  const totalPages = data ? Math.ceil(data.total / PER_PAGE) : 0;

  function reset(fn: () => void) {
    fn();
    setPage(1);
  }

  return (
    <div className="space-y-4">
      <StatusTiles stats={stats} active={status} onPick={(key) => reset(() => setStatus(status === key ? "" : key))} />

      <p className="text-xs text-muted-foreground">
        {stats?.retention_days
          ? `Recent delivery history: resolved notifications are removed after ${stats.retention_days} days.`
          : "Recent delivery history. Resolved notifications are removed on a retention schedule."}
      </p>

      <Filters
        recipient={recipientInput}
        status={status}
        category={category}
        onRecipient={(v) => reset(() => setRecipientInput(v))}
        onStatus={(v) => reset(() => setStatus(v))}
        onCategory={(v) => reset(() => setCategory(v))}
      />

      {error && (
        <div className="flex items-center gap-2 rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700 dark:border-red-900 dark:bg-red-950/30 dark:text-red-400">
          <AlertCircle className="h-4 w-4 shrink-0" />
          Failed to load notification history.
        </div>
      )}

      <NotificationTable isLoading={isLoading} rows={data?.data} onSelect={setSelected} />

      {totalPages > 1 && (
        <Pager page={page} totalPages={totalPages} total={data?.total ?? 0} onPage={setPage} />
      )}

      {selected && <NotificationDetail row={selected} onClose={() => setSelected(null)} />}
    </div>
  );
}

// StatusTiles is the at-a-glance health read. Each tile doubles as a filter,
// since "7 failed" is a number an admin immediately wants the rows behind.
function StatusTiles({
  stats,
  active,
  onPick,
}: {
  stats?: NotificationStats;
  active: string;
  onPick: (key: NotificationStatus) => void;
}) {
  return (
    <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
      {STAT_TILES.map((tile) => (
        <button
          key={tile.key}
          type="button"
          title={tile.help}
          onClick={() => onPick(tile.key)}
          className={`rounded-lg border bg-card p-3 text-left transition-colors hover:bg-muted/50 ${
            active === tile.key ? "border-primary/40 ring-1 ring-primary/30" : ""
          }`}
        >
          <div className="text-xs text-muted-foreground">{tile.label}</div>
          <div className="mt-1 text-2xl font-semibold tabular-nums">
            {stats ? stats[tile.key] : "-"}
          </div>
        </button>
      ))}
    </div>
  );
}

function Filters({
  recipient,
  status,
  category,
  onRecipient,
  onStatus,
  onCategory,
}: {
  recipient: string;
  status: string;
  category: string;
  onRecipient: (v: string) => void;
  onStatus: (v: string) => void;
  onCategory: (v: string) => void;
}) {
  return (
    <div className="flex flex-wrap gap-2">
      <input
        type="text"
        value={recipient}
        onChange={(e) => onRecipient(e.target.value)}
        placeholder="Filter by recipient"
        aria-label="Filter by recipient"
        className="rounded-md border bg-background px-3 py-1.5 text-sm outline-none ring-ring focus:ring-2"
      />
      <select
        value={status}
        onChange={(e) => onStatus(e.target.value)}
        aria-label="Filter by status"
        className="rounded-md border bg-background px-3 py-1.5 text-sm outline-none ring-ring focus:ring-2"
      >
        <option value="">All statuses</option>
        {STAT_TILES.map((t) => (
          <option key={t.key} value={t.key}>
            {t.label}
          </option>
        ))}
      </select>
      <select
        value={category}
        onChange={(e) => onCategory(e.target.value)}
        aria-label="Filter by category"
        className="rounded-md border bg-background px-3 py-1.5 text-sm outline-none ring-ring focus:ring-2"
      >
        <option value="">All categories</option>
        {CATEGORIES.map((c) => (
          <option key={c.value} value={c.value}>
            {c.label}
          </option>
        ))}
      </select>
    </div>
  );
}

function NotificationTable({
  isLoading,
  rows,
  onSelect,
}: {
  isLoading: boolean;
  rows?: NotificationRow[];
  onSelect: (row: NotificationRow) => void;
}) {
  return (
    <div className="overflow-auto rounded-lg border bg-card">
      <table className="w-full text-sm">
        <thead>
          <tr className="border-b bg-muted/50">
            <th className="px-3 py-2 text-left font-medium">Queued</th>
            <th className="px-3 py-2 text-left font-medium">Recipient</th>
            <th className="px-3 py-2 text-left font-medium">Subject</th>
            <th className="px-3 py-2 text-left font-medium">Category</th>
            <th className="px-3 py-2 text-center font-medium">Status</th>
            <th className="px-3 py-2 text-right font-medium">Attempts</th>
            <th className="px-3 py-2 text-left font-medium">Sent</th>
          </tr>
        </thead>
        <tbody>
          {isLoading && (
            <tr>
              <td colSpan={7} className="px-3 py-8 text-center text-muted-foreground">
                Loading...
              </td>
            </tr>
          )}
          {rows?.map((row) => (
            <tr
              key={row.id}
              onClick={() => onSelect(row)}
              className="cursor-pointer border-b transition-colors hover:bg-muted/50"
            >
              <td className="px-3 py-2 text-xs">{new Date(row.created_at).toLocaleString()}</td>
              <td className="px-3 py-2">{row.recipient}</td>
              <td className="max-w-md truncate px-3 py-2 text-xs" title={row.subject}>
                {row.subject}
              </td>
              <td className="px-3 py-2 text-xs">
                {row.category}
                {row.digest && <span className="ml-1 text-muted-foreground">(digest)</span>}
              </td>
              <td className="px-3 py-2 text-center">
                <StatusBadge variant={STATUS_VARIANT[row.status] ?? "neutral"}>
                  {row.status}
                </StatusBadge>
              </td>
              <td className="px-3 py-2 text-right tabular-nums">{row.attempts}</td>
              <td className="px-3 py-2 text-xs">
                {row.sent_at ? new Date(row.sent_at).toLocaleString() : "-"}
              </td>
            </tr>
          ))}
          {rows?.length === 0 && (
            <tr>
              <td colSpan={7} className="px-3 py-8 text-center text-muted-foreground">
                No notifications match these filters.
              </td>
            </tr>
          )}
        </tbody>
      </table>
    </div>
  );
}

function Pager({
  page,
  totalPages,
  total,
  onPage,
}: {
  page: number;
  totalPages: number;
  total: number;
  onPage: (fn: (p: number) => number) => void;
}) {
  return (
    <div className="flex items-center justify-between text-sm">
      <span className="text-muted-foreground">
        Showing {(page - 1) * PER_PAGE + 1}-{Math.min(page * PER_PAGE, total)} of {total}
      </span>
      <div className="flex gap-2">
        <button
          onClick={() => onPage((p) => Math.max(1, p - 1))}
          disabled={page <= 1}
          className="rounded-md border px-3 py-1 text-xs disabled:opacity-50"
        >
          Previous
        </button>
        <button
          onClick={() => onPage((p) => Math.min(totalPages, p + 1))}
          disabled={page >= totalPages}
          className="rounded-md border px-3 py-1 text-xs disabled:opacity-50"
        >
          Next
        </button>
      </div>
    </div>
  );
}

/**
 * NotificationDetail is the drill-in on one queue row. Its reason for existing
 * is the failure case: the error the mail server returned, and how many
 * attempts it took before the queue gave up.
 */
function NotificationDetail({ row, onClose }: { row: NotificationRow; onClose: () => void }) {
  return (
    <div className="fixed inset-0 z-50 flex items-start justify-end bg-black/30" onClick={onClose}>
      <div
        role="dialog"
        aria-label="Notification detail"
        onClick={(e) => e.stopPropagation()}
        className="h-full w-full max-w-md overflow-auto border-l bg-card p-5 shadow-lg"
      >
        <div className="mb-4 flex items-start justify-between gap-3">
          <h3 className="text-sm font-semibold">{row.subject}</h3>
          <button
            type="button"
            onClick={onClose}
            aria-label="Close"
            className="rounded-md p-1 hover:bg-accent"
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        <dl className="space-y-3 text-sm">
          <Field label="Recipient" value={row.recipient} />
          <Field label="Category" value={row.digest ? `${row.category} (daily digest)` : row.category} />
          <Field
            label="Status"
            value={
              <StatusBadge variant={STATUS_VARIANT[row.status] ?? "neutral"}>{row.status}</StatusBadge>
            }
          />
          <Field label="Attempts" value={String(row.attempts)} />
          <Field label="Queued" value={new Date(row.created_at).toLocaleString()} />
          <Field label="Scheduled" value={new Date(row.scheduled_for).toLocaleString()} />
          {row.sent_at && <Field label="Sent" value={new Date(row.sent_at).toLocaleString()} />}
          {row.item_title && <Field label="Item" value={row.item_title} />}
          {row.actor && <Field label="From" value={row.actor} />}
        </dl>

        {row.last_error && (
          <div className="mt-4">
            <div className="text-xs font-medium text-muted-foreground">Last error</div>
            <pre className="mt-1 overflow-auto whitespace-pre-wrap break-words rounded-md border bg-muted/40 p-3 text-xs">
              {row.last_error}
            </pre>
          </div>
        )}
      </div>
    </div>
  );
}

function Field({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div>
      <dt className="text-xs font-medium text-muted-foreground">{label}</dt>
      <dd className="mt-0.5 break-words">{value}</dd>
    </div>
  );
}
