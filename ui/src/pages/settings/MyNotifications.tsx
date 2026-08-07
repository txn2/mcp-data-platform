import { useMemo, useState } from "react";
import { useMyNotifications } from "@/api/portal/hooks";
import type { NotificationItem, NotificationStatus } from "@/api/portal/hooks";
import { StatusBadge } from "@/components/cards/StatusBadge";
import { Button } from "@/components/ui/button";
import { History } from "lucide-react";
import { SettingsCard } from "./panels";
import { ErrorBanner } from "./settingsChrome";

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

// describeRetention states the window this list covers. The server reports the
// retention period only when it has one, so a deployment that has not set it
// gets the honest vaguer sentence rather than "removed after 0 days".
function describeRetention(retentionDays: number): string {
  if (retentionDays) {
    return `What the platform has sent you. Notifications are removed after ${retentionDays} days, so this is recent activity rather than a full record.`;
  }
  return "What the platform has sent you. Older notifications are removed on a retention schedule.";
}

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
  const retentionDays = data?.retention_days ?? 0;

  return (
    <SettingsCard
      icon={History}
      title="Recent notifications"
      description={describeRetention(retentionDays)}
      feedback={
        error && (
          <ErrorBanner message="Failed to load your notifications. The server may be unavailable." />
        )
      }
      contentClassName="p-0"
    >
      <Body isLoading={isLoading} items={data?.data ?? []} />

      {totalPages > 1 && (
        <Pager
          page={page}
          totalPages={totalPages}
          total={data?.total ?? 0}
          onPage={setPage}
        />
      )}
    </SettingsCard>
  );
}

// Body renders the three states the list can be in. An empty history says why
// it is empty rather than leaving a blank panel.
function Body({ isLoading, items }: { isLoading: boolean; items: NotificationItem[] }) {
  if (isLoading) {
    return <p className="p-5 text-sm text-muted-foreground">Loading...</p>;
  }
  if (items.length === 0) {
    return (
      <p className="p-5 text-sm text-muted-foreground">
        No notifications yet. When someone shares something with you or replies to
        your feedback, it will appear here.
      </p>
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
    <div className="flex items-center justify-between border-t px-5 py-3">
      <span className="text-xs text-muted-foreground">
        Showing {(page - 1) * PER_PAGE + 1}-{Math.min(page * PER_PAGE, total)} of {total}
      </span>
      <div className="flex gap-2">
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={() => onPage((p) => Math.max(1, p - 1))}
          disabled={page <= 1}
        >
          Previous
        </Button>
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={() => onPage((p) => Math.min(totalPages, p + 1))}
          disabled={page >= totalPages}
        >
          Next
        </Button>
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
