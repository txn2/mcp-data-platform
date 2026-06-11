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

// Tailwind classes for a kind chip (light + dark).
export const KIND_CHIP: Record<ThreadKind, string> = {
  comment: "bg-muted text-muted-foreground",
  question: "bg-blue-100 text-blue-700 dark:bg-blue-950 dark:text-blue-300",
  correction: "bg-amber-100 text-amber-700 dark:bg-amber-950 dark:text-amber-300",
  suggestion: "bg-violet-100 text-violet-700 dark:bg-violet-950 dark:text-violet-300",
  rating: "bg-yellow-100 text-yellow-700 dark:bg-yellow-950 dark:text-yellow-300",
  approval: "bg-green-100 text-green-700 dark:bg-green-950 dark:text-green-300",
  rejection: "bg-red-100 text-red-700 dark:bg-red-950 dark:text-red-300",
};

export const STATUS_LABEL: Record<ThreadStatus, string> = {
  open: "Open",
  answered: "Answered",
  resolved: "Resolved",
  wont_fix: "Won't fix",
  acknowledged: "Acknowledged",
};

export const STATUS_CHIP: Record<ThreadStatus, string> = {
  open: "bg-blue-100 text-blue-700 dark:bg-blue-950 dark:text-blue-300",
  answered: "bg-violet-100 text-violet-700 dark:bg-violet-950 dark:text-violet-300",
  resolved: "bg-green-100 text-green-700 dark:bg-green-950 dark:text-green-300",
  wont_fix: "bg-muted text-muted-foreground",
  acknowledged: "bg-muted text-muted-foreground",
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
