import { AlertCircle, MessageSquare } from "lucide-react";
import { useFeedbackActivity } from "@/api/portal/hooks";
import type { ThreadActivityItem } from "@/api/portal/types";
import { cn } from "@/lib/utils";
import { KIND_CHIP, KIND_LABEL, STATUS_CHIP, STATUS_LABEL, formatRelative } from "./meta";
import { targetMeta } from "./targetRoute";

interface Props {
  onOpenThread: (id: string) => void;
  onNavigate: (path: string) => void;
}

// ActivityFeed is the unified "recent feedback" view (#617): every thread on an
// asset, collection, or prompt the caller can access, newest first. With no push
// notifications, this is how a user notices new feedback on their work.
export function ActivityFeed({ onOpenThread, onNavigate }: Props) {
  const { data, isLoading, isError } = useFeedbackActivity();
  const items = data?.data ?? [];

  if (isLoading) {
    return <p className="p-6 text-sm text-muted-foreground">Loading feedback…</p>;
  }
  if (isError) {
    return <p className="p-6 text-sm text-destructive">Failed to load feedback activity.</p>;
  }
  if (items.length === 0) {
    return <EmptyState />;
  }

  return (
    <ul className="divide-y">
      {items.map((item) => (
        <ActivityRow
          key={item.id}
          item={item}
          onOpen={() => onOpenThread(item.id)}
          onNavigate={onNavigate}
        />
      ))}
    </ul>
  );
}

function ActivityRow({
  item,
  onOpen,
  onNavigate,
}: {
  item: ThreadActivityItem;
  onOpen: () => void;
  onNavigate: (path: string) => void;
}) {
  const meta = targetMeta(item);
  const replies = Math.max(0, item.event_count - 1);
  const needsResolution =
    item.requires_resolution && item.status !== "resolved" && item.status !== "wont_fix";

  return (
    <li>
      <div
        role="button"
        tabIndex={0}
        onClick={onOpen}
        onKeyDown={(e) => {
          if (e.key === "Enter" || e.key === " ") {
            e.preventDefault();
            onOpen();
          }
        }}
        className="group flex cursor-pointer gap-3 px-4 py-3 transition-colors hover:bg-accent/50"
      >
        <div className="mt-0.5 flex h-9 w-9 shrink-0 items-center justify-center rounded-lg border bg-muted/40 text-muted-foreground group-hover:text-foreground">
          <meta.Icon className="h-4 w-4" />
        </div>
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-1.5">
            {meta.route ? (
              <button
                type="button"
                onClick={(e) => {
                  e.stopPropagation();
                  onNavigate(meta.route!);
                }}
                className="truncate text-sm font-medium hover:underline"
                title={`Go to ${meta.label.toLowerCase()}: ${item.target_label}`}
              >
                {item.target_label}
              </button>
            ) : (
              <span className="truncate text-sm font-medium">{item.target_label}</span>
            )}
            <span className="shrink-0 text-[10px] font-medium uppercase tracking-wide text-muted-foreground">
              {meta.label}
            </span>
            {needsResolution && (
              <AlertCircle className="h-3.5 w-3.5 shrink-0 text-amber-500" aria-label="Needs resolution" />
            )}
            <span className="ml-auto shrink-0 text-[11px] text-muted-foreground">
              {formatRelative(item.last_event_at)}
            </span>
          </div>
          <p className="mt-0.5 truncate text-sm text-foreground/90">
            {item.title || "(untitled feedback)"}
          </p>
          <div className="mt-1 flex items-center gap-1.5">
            <span className={cn("rounded-full px-1.5 py-0.5 text-[11px] font-medium", KIND_CHIP[item.kind])}>
              {KIND_LABEL[item.kind]}
            </span>
            <span className={cn("rounded-full px-1.5 py-0.5 text-[11px] font-medium", STATUS_CHIP[item.status])}>
              {STATUS_LABEL[item.status]}
            </span>
            <span className="min-w-0 truncate text-[11px] text-muted-foreground">
              {item.author_email}
              {replies > 0 ? ` · ${replies} ${replies === 1 ? "reply" : "replies"}` : ""}
            </span>
          </div>
        </div>
      </div>
    </li>
  );
}

function EmptyState() {
  return (
    <div className="flex flex-col items-center justify-center gap-2 py-16 text-center">
      <MessageSquare className="h-8 w-8 text-muted-foreground/40" />
      <p className="text-sm font-medium">No feedback yet</p>
      <p className="max-w-sm text-xs text-muted-foreground">
        Feedback on assets, collections, and prompts you own or can access shows up here, newest first.
      </p>
    </div>
  );
}
