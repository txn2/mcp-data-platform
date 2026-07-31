import { useQuery } from "@tanstack/react-query";
import { apiFetch } from "../client";

// --- Notification delivery monitoring (#1016) ---

export type NotificationStatus = "pending" | "sending" | "sent" | "failed";

/**
 * NotificationRow is one queue row as the admin tab shows it: the delivery
 * record plus the subject line the recipient's email carried, so a reported
 * message can be matched to its row.
 */
export interface NotificationRow {
  id: number;
  recipient: string;
  category: string;
  subject: string;
  digest: boolean;
  status: NotificationStatus;
  attempts: number;
  last_error?: string;
  item_title?: string;
  actor?: string;
  link?: string;
  scheduled_for: string;
  sent_at?: string;
  created_at: string;
}

export interface NotificationList {
  data: NotificationRow[];
  total: number;
  page: number;
  per_page: number;
}

export interface NotificationStats {
  pending: number;
  sending: number;
  sent: number;
  failed: number;
  total: number;
  // retention_days is how long a resolved row survives the queue's purge.
  // Zero means the deployment reported no window.
  retention_days: number;
}

export interface NotificationQuery {
  recipient?: string;
  status?: string;
  category?: string;
  page?: number;
  per_page?: number;
}

// notificationParams builds the shared query string. The list and the stats
// read the same filters, so they build them the same way; the server ignores
// the ones an endpoint does not apply.
function notificationParams(query: NotificationQuery): string {
  const params = new URLSearchParams();
  if (query.recipient) params.set("recipient", query.recipient);
  if (query.status) params.set("status", query.status);
  if (query.category) params.set("category", query.category);
  if (query.page && query.page > 1) params.set("page", String(query.page));
  if (query.per_page) params.set("per_page", String(query.per_page));
  const qs = params.toString();
  return qs ? `?${qs}` : "";
}

export function useNotifications(query: NotificationQuery = {}) {
  return useQuery({
    queryKey: ["admin-notifications", query],
    queryFn: () => apiFetch<NotificationList>(`/notifications${notificationParams(query)}`),
  });
}

// useNotificationStats reads the per-status breakdown. The status filter is
// deliberately dropped: a list narrowed to failures still shows how many sent.
export function useNotificationStats(query: NotificationQuery = {}) {
  const scoped = { recipient: query.recipient, category: query.category };
  return useQuery({
    queryKey: ["admin-notification-stats", scoped],
    queryFn: () => apiFetch<NotificationStats>(`/notifications/stats${notificationParams(scoped)}`),
  });
}
