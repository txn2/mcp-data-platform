import type { ThreadKind, ThreadStatus } from "@/api/portal/types";

// Kind metadata: the seven authoring kinds a human can open a thread with.
export const THREAD_KINDS: { value: ThreadKind; label: string }[] = [
  { value: "comment", label: "Comment" },
  { value: "question", label: "Question" },
  { value: "correction", label: "Correction" },
  { value: "suggestion", label: "Suggestion" },
  { value: "rating", label: "Rating" },
  { value: "approval", label: "Approval" },
  { value: "rejection", label: "Rejection" },
];

export const KIND_LABEL: Record<ThreadKind, string> = Object.fromEntries(
  THREAD_KINDS.map((k) => [k.value, k.label]),
) as Record<ThreadKind, string>;

export const STATUS_LABEL: Record<ThreadStatus, string> = {
  open: "Open",
  answered: "Answered",
  resolved: "Resolved",
  wont_fix: "Won't fix",
  acknowledged: "Acknowledged",
};

// Statuses a moderator can transition a thread to from the detail view.
export const MODERATION_STATUSES: ThreadStatus[] = [
  "open",
  "answered",
  "resolved",
  "wont_fix",
  "acknowledged",
];

// formatRelative renders an ISO timestamp as a short relative string.
export function formatRelative(iso: string): string {
  const then = new Date(iso).getTime();
  if (Number.isNaN(then)) return "";
  const secs = Math.round((Date.now() - then) / 1000);
  if (secs < 60) return "just now";
  const mins = Math.round(secs / 60);
  if (mins < 60) return `${mins}m ago`;
  const hours = Math.round(mins / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.round(hours / 24);
  if (days < 30) return `${days}d ago`;
  return new Date(iso).toLocaleDateString();
}
