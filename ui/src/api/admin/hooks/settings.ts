import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { apiFetch } from "../client";

// --- Platform settings (SMTP, #631) ---

// SMTPSettings is the stored SMTP configuration as returned by the server.
// The password is never returned; password_set reports whether one is stored.
export interface SMTPSettings {
  enabled: boolean;
  host: string;
  port: number;
  username: string;
  password_set: boolean;
  from: string;
  from_name: string;
  tls_mode: string;
  updated_by?: string;
  updated_at?: string;
  // warnings describes accepted-but-hazardous combinations in the stored
  // configuration, e.g. credentials configured with TLS off (#1072). Absent
  // when the stored configuration raises none.
  warnings?: string[];
}

// SMTPSettingsInput is the PUT body. The password field is write-only:
// an empty password keeps the stored one.
export interface SMTPSettingsInput {
  enabled: boolean;
  host: string;
  port: number;
  username: string;
  password: string;
  from: string;
  from_name: string;
  tls_mode: string;
}

export function useSMTPSettings() {
  return useQuery({
    queryKey: ["settings", "smtp"],
    queryFn: () => apiFetch<SMTPSettings>("/settings/smtp"),
  });
}

export function useSetSMTPSettings() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: SMTPSettingsInput) =>
      apiFetch<SMTPSettings>("/settings/smtp", {
        method: "PUT",
        body: JSON.stringify(input),
      }),
    // Seed the cache from the response so the form re-syncs to the stored
    // state (e.g. password_set flipping true) without a refetch round-trip.
    onSuccess: (data) => {
      qc.setQueryData(["settings", "smtp"], data);
    },
  });
}

// SMTPRecipientStatus reports whether a test-send target has opted out of
// notification emails (#1022). Informational only; a test still sends.
export interface SMTPRecipientStatus {
  to: string;
  opted_out: boolean;
}

// useSMTPRecipientStatus checks the opt-out state of a test-send target. It
// only fires for a plausible address, so keystrokes short of one cost nothing.
export function useSMTPRecipientStatus(to: string) {
  const plausible = /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(to);
  return useQuery({
    queryKey: ["settings", "smtp", "recipient-status", to],
    queryFn: () =>
      apiFetch<SMTPRecipientStatus>(
        `/settings/smtp/recipient-status?to=${encodeURIComponent(to)}`,
      ),
    enabled: plausible,
  });
}

export function useSendTestEmail() {
  return useMutation({
    mutationFn: (to: string) =>
      apiFetch<{ status: string; to: string }>("/settings/smtp/test", {
        method: "POST",
        body: JSON.stringify({ to }),
      }),
  });
}
