import { useMemo, useState } from "react";
import { useMyNotifications } from "@/api/portal/hooks";
import type { NotificationItem, NotificationStatus } from "@/api/portal/hooks";
import { StatusBadge } from "@/components/cards/StatusBadge";
import { History } from "lucide-react";

const PER_PAGE = 20;

const STATUS_VARIANT: Record<NotificationStatus, "success" | "error" | "warning" | "neutral"> = {
  sent: "success",
  failed: "error",
  sending: "warning",
  pending: "neutral",
};

// STATUS_LABEL says what a status means to the person waiting on the email,
// rather than naming the queue state it came from.
const STATUS_LABEL: Record<NotificationStatus, string> = {
  sent: "Sent",
  failed: "Not delivered",
  sending: "Sending",
  pending: "Queued",
};

const CATEGORY_LABEL: Record<string, string> = {
  share: "Share",
  comment: "Comment",
  mention: "Mention",
};

/**
 * MyNotifications shows the notifications the platform has addressed to the
 * signed-in user: what was sent, what is still queued, and what never went
 * out. It sits with the notification preferences because the two answer one
 * question together -- what should I be told, and what was I actually told.
 *
 * The endpoint behind it is self-scoped server-side, so there is nothing here
 * to choose whose activity to view. It shows no delivery-error text either:
 * a failure is the platform's to fix, not the recipient's.
 */
export function MyNotifications() {
  const [page, setPage] = useState(1);
  const query = useMemo(() => ({ page, per_page: PER_PAGE }), [page]);
  const { data, isLoading, error } = useMyNotifications(query);
  const totalPages = data ? Math.ceil(data.total / PER_PAGE) : 0;

  return (
    <div className="overflow-hidden rounded-lg border bg-card">
      <Header retentionDays={data?.retention_days ?? 0} />

      {error && (
        <div className="border-b bg-red-50 px-5 py-2.5 text-xs text-red-700 dark:bg-red-950/30 dark:text-red-400">
          Failed to load your notifications. The server may be unavailable.
        </div>
      )}

      <Body isLoading={isLoading} items={data?.data ?? []} />

      {totalPages > 1 && (
        <Pager
          page={page}
          totalPages={totalPages}
          total={data?.total ?? 0}
          onPage={setPage}
        />
      )}
    </div>
  );
}

// Header states what the list is and, when the server reports one, the
// retention window it covers.
function Header({ retentionDays }: { retentionDays: number }) {
  return (
    <div className="flex items-center gap-3 border-b px-5 py-3">
      <History className="h-4 w-4 text-muted-foreground" />
      <div>
        <h3 className="text-sm font-semibold leading-none">Recent notifications</h3>
        <p className="mt-1 text-xs text-muted-foreground">
          {retentionDays
            ? `What the platform has sent you. Notifications are removed after ${retentionDays} days, so this is recent activity rather than a full record.`
            : "What the platform has sent you. Older notifications are removed on a retention schedule."}
        </p>
      </div>
    </div>
  );
}

// Body renders the three states the list can be in. An empty history says why
// it is empty rather than leaving a blank panel.
function Body({ isLoading, items }: { isLoading: boolean; items: NotificationItem[] }) {
  if (isLoading) {
    return <div className="p-5 text-sm text-muted-foreground">Loading...</div>;
  }
  if (items.length === 0) {
    return (
      <div className="p-5 text-sm text-muted-foreground">
        No notifications yet. When someone shares something with you or replies to
        your feedback, it will appear here.
      </div>
    );
  }
  return (
    <ul className="divide-y">
      {items.map((item) => (
        <NotificationRow key={item.id} item={item} />
      ))}
    </ul>
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
    <div className="flex items-center justify-between border-t px-5 py-3 text-sm">
      <span className="text-xs text-muted-foreground">
        Showing {(page - 1) * PER_PAGE + 1}-{Math.min(page * PER_PAGE, total)} of {total}
      </span>
      <div className="flex gap-2">
        <button
          type="button"
          onClick={() => onPage((p) => Math.max(1, p - 1))}
          disabled={page <= 1}
          className="rounded-md border px-3 py-1 text-xs disabled:opacity-50"
        >
          Previous
        </button>
        <button
          type="button"
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

function NotificationRow({ item }: { item: NotificationItem }) {
  // A queued row has no send time yet; showing when it was raised is more
  // useful than showing nothing.
  const when = item.sent_at ?? item.created_at;
  return (
    <li className="flex items-start justify-between gap-3 px-5 py-3">
      <div className="min-w-0">
        <p className="truncate text-sm">
          {item.link ? (
            <a href={item.link} className="hover:underline">
              {item.subject}
            </a>
          ) : (
            item.subject
          )}
        </p>
        <p className="mt-0.5 text-xs text-muted-foreground">
          {CATEGORY_LABEL[item.category] ?? item.category}
          {item.digest && " - daily digest"} - {new Date(when).toLocaleString()}
        </p>
      </div>
      <StatusBadge variant={STATUS_VARIANT[item.status] ?? "neutral"}>
        {STATUS_LABEL[item.status] ?? item.status}
      </StatusBadge>
    </li>
  );
}
