import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { apiFetch } from "../client";

// --- Email notification preferences (#631) ---

export type NotificationMode = "off" | "immediate" | "daily";

export interface NotificationPrefs {
  mode: NotificationMode;
  shares_enabled: boolean;
  comments_enabled: boolean;
  mentions_enabled: boolean;
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
    mutationFn: (input: Partial<NotificationPrefs>) =>
      apiFetch<NotificationPrefs>("/notification-prefs", {
        method: "PUT",
        body: JSON.stringify(input),
      }),
    onSuccess: (data) => {
      qc.setQueryData(["notification-prefs"], data);
    },
  });
}
