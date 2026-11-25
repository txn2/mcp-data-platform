import type { PlatformFeedbackStatus } from "@/api/admin/hooks";

// How a post's status reads and is tinted on the Platform feedback pages.

export const STATUS_LABELS: Record<PlatformFeedbackStatus, string> = {
  open: "Open",
  planned: "Planned",
  done: "Done",
  declined: "Declined",
};

export function statusVariant(status: string): "info" | "success" | "muted" | "secondary" {
  switch (status) {
    case "open":
      return "info";
    case "planned":
      return "secondary";
    case "done":
      return "success";
    default:
      return "muted";
  }
}

export function statusLabel(status: string): string {
  return STATUS_LABELS[status as PlatformFeedbackStatus] ?? status;
}

/** deliveryLabel is how a channel's send reads: Sent, Failed or Waiting. */
export function deliveryLabel(status: string): { label: string; variant: "success" | "danger" | "muted" } {
  if (status === "sent") return { label: "Sent", variant: "success" };
  if (status === "failed") return { label: "Failed", variant: "danger" };
  return { label: "Waiting", variant: "muted" };
}
