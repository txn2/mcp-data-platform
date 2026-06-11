import { useState } from "react";
import { X, Plus } from "lucide-react";
import { useThreads } from "@/api/portal/hooks";
import type { FeedbackTarget } from "@/api/portal/types";
import { ThreadList } from "./ThreadList";
import { ThreadDetail } from "./ThreadDetail";
import { NewThreadForm } from "./NewThreadForm";
import { useTextQuoteAnchor } from "./useTextQuoteAnchor";
import { filterForTarget, targetLabel } from "./targetFilter";

interface Props {
  target: FeedbackTarget;
  canModerate: boolean;
  // When omitted (e.g. the full-page standalone channel) no close button shows.
  onClose?: () => void;
}

type View = { kind: "list" } | { kind: "new" } | { kind: "detail"; threadId: string };

export function FeedbackPanel({ target, canModerate, onClose }: Props) {
  const filter = filterForTarget(target);
  const { data, isLoading } = useThreads(filter);
  const { availableAnchor } = useTextQuoteAnchor();
  const [view, setView] = useState<View>({ kind: "list" });

  const threads = data?.data ?? [];
  const openCount = threads.filter((t) => t.status === "open").length;
  const needsResolution = threads.filter(
    (t) => t.requires_resolution && t.status !== "resolved" && t.status !== "wont_fix",
  ).length;

  return (
    <div className="flex h-full w-full flex-col bg-card">
      {/* Header */}
      <div className="flex items-center gap-2 border-b p-3">
        <div className="min-w-0">
          <h2 className="truncate text-sm font-semibold">{targetLabel(target)}</h2>
          <p className="text-xs text-muted-foreground">
            {openCount} open
            {needsResolution > 0 ? ` · ${needsResolution} need resolution` : ""}
          </p>
        </div>
        {view.kind === "list" && (
          <button
            type="button"
            onClick={() => setView({ kind: "new" })}
            className="ml-auto flex items-center gap-1 rounded-md bg-primary px-2.5 py-1.5 text-xs font-medium text-primary-foreground hover:bg-primary/90"
          >
            <Plus className="h-3.5 w-3.5" /> New
          </button>
        )}
        {onClose && (
          <button
            type="button"
            onClick={onClose}
            className={`rounded p-1 hover:bg-accent ${view.kind === "list" ? "" : "ml-auto"}`}
            aria-label="Close feedback"
          >
            <X className="h-4 w-4" />
          </button>
        )}
      </div>

      {/* Body */}
      <div className="min-h-0 flex-1 overflow-auto">
        {view.kind === "list" && (
          <ThreadList
            threads={threads}
            isLoading={isLoading}
            onSelect={(threadId) => setView({ kind: "detail", threadId })}
          />
        )}
        {view.kind === "new" && (
          <NewThreadForm
            target={target}
            availableAnchor={availableAnchor}
            onCancel={() => setView({ kind: "list" })}
            onCreated={(threadId) => setView({ kind: "detail", threadId })}
          />
        )}
        {view.kind === "detail" && (
          <ThreadDetail
            threadId={view.threadId}
            canModerate={canModerate}
            onBack={() => setView({ kind: "list" })}
            onDeleted={() => setView({ kind: "list" })}
          />
        )}
      </div>
    </div>
  );
}
