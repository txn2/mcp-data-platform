import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiFetch, apiFetchRaw } from "../client";

// Platform feedback (#2820): what agents report the platform could not do for
// them, the settings that let them post, and the posts an administrator
// reads and answers.

export type PlatformFeedbackStatus = "open" | "planned" | "done" | "declined";

export const PLATFORM_FEEDBACK_STATUSES: PlatformFeedbackStatus[] = ["open", "planned", "done", "declined"];

export interface PlatformFeedbackSettings {
  enabled: boolean;
  /** The notification channels a new post is sent to. */
  channels: string[];
  /** Personas that may post; empty is every persona except service accounts. */
  personas: string[];
  session_limit: number;
  persona_daily_limit: number;
}

export interface PlatformFeedbackSettingsView extends PlatformFeedbackSettings {
  updated_by?: string;
  updated_at?: string;
}

export interface PlatformFeedbackItem {
  id: string;
  trying_to: string;
  blocked_by: string;
  tool: string;
  suggestion: string;
  status: PlatformFeedbackStatus;
  report_count: number;
  created_at: string;
  last_reported_at: string;
}

export interface PlatformFeedbackStats {
  open: number;
  reports_this_week: number;
  refused_this_week: number;
  delivery_failures: number;
}

export interface PlatformFeedbackList {
  posts: PlatformFeedbackItem[];
  total: number;
  stats: PlatformFeedbackStats;
}

export interface PlatformFeedbackReport {
  persona: string;
  calls: string[] | null;
  reported_at: string;
}

export interface PlatformFeedbackDelivery {
  channel: string;
  /** The notification's state: pending, sending, sent or failed. */
  status: string;
  attempts: number;
  last_error?: string;
  sent_at?: string;
}

export interface PlatformFeedbackDetail extends PlatformFeedbackItem {
  reports: PlatformFeedbackReport[];
  deliveries: PlatformFeedbackDelivery[];
}

export interface PlatformFeedbackFilter {
  status?: string;
  tool?: string;
  q?: string;
  limit?: number;
  offset?: number;
}

// noContent sends a change the server answers with 204, and throws the
// server's refusal.
async function noContent(path: string, method: string, body: unknown): Promise<void> {
  const res = await apiFetchRaw(path, {
    method,
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!res.ok) {
    const b = (await res.json().catch(() => ({}))) as { detail?: string };
    throw new Error(b.detail || `request failed: ${res.status}`);
  }
}

const SETTINGS = ["settings", "platform-feedback"] as const;
const SETTINGS_PATH = "/settings/platform-feedback";
const POSTS = ["platform-feedback"] as const;
const POSTS_PATH = "/platform-feedback";

export function usePlatformFeedbackSettings() {
  return useQuery({
    queryKey: SETTINGS,
    queryFn: () => apiFetch<PlatformFeedbackSettingsView>(SETTINGS_PATH),
  });
}

export function useSetPlatformFeedbackSettings() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: PlatformFeedbackSettings) =>
      apiFetch<PlatformFeedbackSettingsView>(SETTINGS_PATH, { method: "PUT", body: JSON.stringify(input) }),
    onSuccess: (data) => qc.setQueryData(SETTINGS, data),
  });
}

export function usePlatformFeedbackList(filter: PlatformFeedbackFilter = {}) {
  const params = new URLSearchParams();
  if (filter.status) params.set("status", filter.status);
  if (filter.tool) params.set("tool", filter.tool);
  if (filter.q) params.set("q", filter.q);
  if (filter.limit) params.set("limit", String(filter.limit));
  if (filter.offset) params.set("offset", String(filter.offset));
  const qs = params.toString();
  return useQuery({
    queryKey: [...POSTS, "list", filter],
    queryFn: () => apiFetch<PlatformFeedbackList>(`${POSTS_PATH}${qs ? `?${qs}` : ""}`),
  });
}

export function usePlatformFeedbackPost(id: string) {
  return useQuery({
    queryKey: [...POSTS, "post", id],
    queryFn: () => apiFetch<PlatformFeedbackDetail>(`${POSTS_PATH}/${encodeURIComponent(id)}`),
  });
}

export function useSetPlatformFeedbackStatus(id: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (status: PlatformFeedbackStatus) =>
      noContent(`${POSTS_PATH}/${encodeURIComponent(id)}/status`, "PUT", { status }),
    onSuccess: () => qc.invalidateQueries({ queryKey: POSTS }),
  });
}

export function useResendPlatformFeedback(id: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (channel: string) =>
      noContent(`${POSTS_PATH}/${encodeURIComponent(id)}/resend`, "POST", { channel }),
    onSuccess: () => qc.invalidateQueries({ queryKey: [...POSTS, "post", id] }),
  });
}
