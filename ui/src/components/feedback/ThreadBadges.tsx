import { useId } from "react";
import { Badge } from "@/components/ui/badge";
import type { ThreadKind, ThreadStatus, ThreadVisibility } from "@/api/portal/types";
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

// ThreadVisibilityBadge says who sees a thread (#2227): Internal is the
// organization's own people, Shared is everyone who can open the item.
export function ThreadVisibilityBadge({ visibility }: { visibility?: ThreadVisibility }) {
  return visibility === "shared" ? (
    <Badge variant="info" className="px-1.5 text-[11px]">
      Shared
    </Badge>
  ) : (
    <Badge variant="outline" className="px-1.5 text-[11px]">
      Internal
    </Badge>
  );
}

// VisibilityChoice is the Internal / Shared choice a thread is opened with
// and a moderator changes (#2227). Each instance names its own radio group,
// so the composer and a thread's header never share one.
export function VisibilityChoice({
  value,
  onChange,
  disabled,
}: {
  value: ThreadVisibility;
  onChange: (v: ThreadVisibility) => void;
  disabled?: boolean;
}) {
  const name = useId();
  return (
    <div role="radiogroup" aria-label="Visibility" className="flex items-center gap-3 text-xs">
      {(["internal", "shared"] as const).map((v) => (
        <label key={v} className="flex items-center gap-1">
          <input
            type="radio"
            name={name}
            value={v}
            checked={value === v}
            disabled={disabled}
            onChange={() => onChange(v)}
          />
          {v === "internal" ? "Internal" : "Shared"}
        </label>
      ))}
    </div>
  );
}
