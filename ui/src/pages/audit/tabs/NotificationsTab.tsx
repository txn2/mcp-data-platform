import { useState, useMemo } from "react";
import { AlertCircle } from "lucide-react";

import { useNotifications, useNotificationStats } from "@/api/admin/hooks";
import type { NotificationRow } from "@/api/admin/hooks";
import { Pager } from "@/components/patterns/Pager";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { useDebounced } from "@/lib/useDebounced";
import { NotificationDetail } from "../notifications/NotificationDetail";
import { NotificationFilters } from "../notifications/NotificationFilters";
import { NotificationTable } from "../notifications/NotificationTable";
import { StatusTiles } from "../notifications/StatusTiles";

const PER_PAGE = 20;

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
  const total = data?.total ?? 0;

  function reset(fn: () => void) {
    fn();
    setPage(1);
  }

  return (
    <div className="space-y-4">
      <StatusTiles
        stats={stats}
        active={status}
        onPick={(key) => reset(() => setStatus(status === key ? "" : key))}
      />

      <p className="text-xs text-muted-foreground">
        {stats?.retention_days
          ? `Recent delivery history: resolved notifications are removed after ${stats.retention_days} days.`
          : "Recent delivery history. Resolved notifications are removed on a retention schedule."}
      </p>

      <NotificationFilters
        recipient={recipientInput}
        status={status}
        category={category}
        onRecipient={(v) => reset(() => setRecipientInput(v))}
        onStatus={(v) => reset(() => setStatus(v))}
        onCategory={(v) => reset(() => setCategory(v))}
      />

      {error && (
        <Alert variant="destructive">
          <AlertCircle />
          <AlertDescription>Failed to load notification history.</AlertDescription>
        </Alert>
      )}

      <NotificationTable isLoading={isLoading} rows={data?.data} onSelect={setSelected} />

      {total > PER_PAGE && (
        <Pager page={page} perPage={PER_PAGE} total={total} onPage={setPage} />
      )}

      {selected && <NotificationDetail row={selected} onClose={() => setSelected(null)} />}
    </div>
  );
}
