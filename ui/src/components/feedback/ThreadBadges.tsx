import { Badge } from "@/components/ui/badge";
import type { ThreadKind, ThreadStatus } from "@/api/portal/types";
import { KIND_LABEL, STATUS_LABEL } from "./meta";

// The feedback vocabulary and the tint each word carries, in one place, so a
// thread reads the same colour in the activity feed, the per-item drawer, the
// worklist, and the thread's own header.

const KIND_VARIANT: Record<ThreadKind, React.ComponentProps<typeof Badge>["variant"]> = {
  comment: "muted",
  question: "info",
  correction: "warning",
  suggestion: "secondary",
  rating: "outline",
  approval: "success",
  rejection: "danger",
};

// Answered is `secondary`, not `success`: an answered thread has had a reply,
// not a resolution, and only `resolved` is the finished state. Won't-fix and
// acknowledged are both closed without a change, so both read as muted.
const STATUS_VARIANT: Record<ThreadStatus, React.ComponentProps<typeof Badge>["variant"]> = {
  open: "info",
  answered: "secondary",
  resolved: "success",
  wont_fix: "muted",
  acknowledged: "muted",
};

export function ThreadKindBadge({ kind }: { kind: ThreadKind }) {
  return (
    <Badge variant={KIND_VARIANT[kind]} className="px-1.5 text-[11px]">
      {KIND_LABEL[kind]}
    </Badge>
  );
}

export function ThreadStatusBadge({ status }: { status: ThreadStatus }) {
  return (
    <Badge variant={STATUS_VARIANT[status]} className="px-1.5 text-[11px]">
      {STATUS_LABEL[status]}
    </Badge>
  );
}
