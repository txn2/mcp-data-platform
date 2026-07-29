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
