import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { apiFetch } from "../client";

// --- Email notification preferences (#631) ---

export type NotificationMode = "off" | "immediate" | "daily";

export interface NotificationPrefs {
  mode: NotificationMode;
  shares_enabled: boolean;
  comments_enabled: boolean;
  mentions_enabled: boolean;
  // delivery_available is server-computed and read-only: false when the
  // platform has no SMTP delivery path, so stored preferences describe an
  // intent nothing can act on (#1099).
  delivery_available: boolean;
}

// NotificationPrefsUpdate is the writable subset: delivery_available reports
// platform state, so it is never sent back.
export type NotificationPrefsUpdate = Omit<NotificationPrefs, "delivery_available">;

// --- My notification history (#1016) ---

export type NotificationStatus = "pending" | "sending" | "sent" | "failed";

/**
 * NotificationItem is one notification as its recipient sees it. It carries no
 * delivery-error text: a failed send fails for reasons that belong to the
 * platform's mail infrastructure, which the recipient cannot act on.
 */
export interface NotificationItem {
  id: number;
  category: string;
  subject: string;
  item_title?: string;
  actor?: string;
  link?: string;
  digest: boolean;
  status: NotificationStatus;
  sent_at?: string;
  created_at: string;
}

export interface NotificationHistory {
  data: NotificationItem[];
  total: number;
  page: number;
  per_page: number;
  // retention_days is the window this history covers; the queue purges
  // resolved rows past it, so the screen shows recent activity rather than a
  // complete record. Zero means the server reported no window.
  retention_days: number;
}

export interface NotificationHistoryQuery {
  status?: string;
  category?: string;
  page?: number;
  per_page?: number;
}

// useMyNotifications reads the caller's own notification history. The endpoint
// is self-scoped server-side: there is no recipient parameter to pass.
export function useMyNotifications(query: NotificationHistoryQuery = {}) {
  const params = new URLSearchParams();
  if (query.status) params.set("status", query.status);
  if (query.category) params.set("category", query.category);
  if (query.page && query.page > 1) params.set("page", String(query.page));
  if (query.per_page) params.set("per_page", String(query.per_page));
  const qs = params.toString();

  return useQuery({
    queryKey: ["my-notifications", query],
    queryFn: () => apiFetch<NotificationHistory>(`/notifications${qs ? `?${qs}` : ""}`),
  });
}

export function useNotificationPrefs() {
  return useQuery({
    queryKey: ["notification-prefs"],
    queryFn: () => apiFetch<NotificationPrefs>("/notification-prefs"),
  });
}

// useSetNotificationPrefs accepts a partial update (only the changed fields)
// and receives the full preference set back; the response seeds the cache so
// the page reflects the stored state without a refetch round-trip.
export function useSetNotificationPrefs() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: Partial<NotificationPrefsUpdate>) =>
      apiFetch<NotificationPrefs>("/notification-prefs", {
        method: "PUT",
        body: JSON.stringify(input),
      }),
    onSuccess: (data) => {
      qc.setQueryData(["notification-prefs"], data);
    },
  });
}
