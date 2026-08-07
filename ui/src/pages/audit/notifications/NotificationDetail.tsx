import { X } from "lucide-react";

import type { NotificationRow } from "@/api/admin/hooks";
import { StatusBadge } from "@/components/cards/StatusBadge";
import { Button } from "@/components/ui/button";
import { STATUS_VARIANT } from "./NotificationTable";

/**
 * NotificationDetail is the drill-in on one queue row. Its reason for existing
 * is the failure case: the error the mail server returned, and how many
 * attempts it took before the queue gave up. Extracted from
 * NotificationsTab.tsx (#1207).
 */
export function NotificationDetail({
  row,
  onClose,
}: {
  row: NotificationRow;
  onClose: () => void;
}) {
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
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            onClick={onClose}
            aria-label="Close"
          >
            <X />
          </Button>
        </div>

        <dl className="space-y-3 text-sm">
          <Field label="Recipient" value={row.recipient} />
          <Field
            label="Category"
            value={row.digest ? `${row.category} (daily digest)` : row.category}
          />
          <Field
            label="Status"
            value={
              <StatusBadge variant={STATUS_VARIANT[row.status] ?? "neutral"}>
                {row.status}
              </StatusBadge>
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
