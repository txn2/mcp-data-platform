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

// --- Knowledge review-queue staleness alert (#803) ---

// ReviewQueueAlertSettings is one review queue's stored alert configuration. A
// threshold of 0 disables that condition; warnings describe a configuration
// that saves cleanly but delivers nothing.
export interface ReviewQueueAlertSettings {
  enabled: boolean;
  pending_threshold: number;
  oldest_pending_days: number;
  cooldown_hours: number;
  recipients: string[];
  updated_by?: string;
  updated_at?: string;
  warnings?: string[];
}

// ReviewQueueAlertInput is the PUT body: the same fields without the
// server-owned audit columns and warnings.
export type ReviewQueueAlertInput = Omit<
  ReviewQueueAlertSettings,
  "updated_by" | "updated_at" | "warnings"
>;

// ReviewAlertQueue is the settings path segment of one review queue. Both
// queues are the same mechanism with their own thresholds and recipients
// (#803, #1287), so they are one pair of hooks over a queue rather than two
// copies of it.
export type ReviewAlertQueue = "review-queue-alert";

export function useReviewAlert(queue: ReviewAlertQueue) {
  return useQuery({
    queryKey: ["settings", queue],
    queryFn: () => apiFetch<ReviewQueueAlertSettings>(`/settings/${queue}`),
  });
}

export function useSetReviewAlert(queue: ReviewAlertQueue) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: ReviewQueueAlertInput) =>
      apiFetch<ReviewQueueAlertSettings>(`/settings/${queue}`, {
        method: "PUT",
        body: JSON.stringify(input),
      }),
    // The PUT answers with the stored state (recipients normalized, warnings
    // re-evaluated), so seed the cache from it rather than refetching.
    onSuccess: (data) => {
      qc.setQueryData(["settings", queue], data);
    },
  });
}

// --- Connection-revocation alert (#1694) ---

// ConnectionAlertSettings is the stored configuration for the alert raised
// when an upstream rejects a connection's refresh and the platform discards
// the credential. Recipients are the escalation only: the person who
// authorized the connection is told regardless, and an empty list is the
// default rather than a misconfiguration.
export interface ConnectionAlertSettings {
  enabled: boolean;
  escalate_after_hours: number;
  recipients: string[];
  updated_by?: string;
  updated_at?: string;
  warnings?: string[];
}

// ConnectionAlertInput is the PUT body: the same fields without the
// server-owned audit columns and warnings.
export type ConnectionAlertInput = Omit<
  ConnectionAlertSettings,
  "updated_by" | "updated_at" | "warnings"
>;

export function useConnectionAlert() {
  return useQuery({
    queryKey: ["settings", "connection-alert"],
    queryFn: () => apiFetch<ConnectionAlertSettings>("/settings/connection-alert"),
  });
}

export function useSetConnectionAlert() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: ConnectionAlertInput) =>
      apiFetch<ConnectionAlertSettings>("/settings/connection-alert", {
        method: "PUT",
        body: JSON.stringify(input),
      }),
    // The PUT answers with the stored state (recipients normalized, warnings
    // re-evaluated), so seed the cache from it rather than refetching.
    onSuccess: (data) => {
      qc.setQueryData(["settings", "connection-alert"], data);
    },
  });
}
