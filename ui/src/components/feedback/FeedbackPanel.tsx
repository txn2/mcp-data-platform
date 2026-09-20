import { useState } from "react";
import { X, Plus } from "lucide-react";
import { useInfiniteThreads } from "@/api/portal/hooks";
import type { FeedbackTarget } from "@/api/portal/types";
import { InfiniteFooter } from "@/components/InfiniteFooter";
import { Button } from "@/components/ui/button";
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
  // flow renders the panel as content that flows into the page rather than as
  // a viewport-height column with its own scroll box. The slide-over wants the
  // column; a tab of the Inbox wants the flow, so the page scrolls in one
  // place like every other portal page (#1798).
  flow?: boolean;
}

type View = { kind: "list" } | { kind: "new" } | { kind: "detail"; threadId: string };

// panelShape is the two layouts this panel has. It is a function rather than
// two ternaries in the body because the body is already at the complexity
// budget, and which shell to wear is not part of what the panel does.
function panelShape(flow?: boolean): { shell: string; body: string } {
  if (flow) {
    return { shell: "flex w-full flex-col bg-card", body: "" };
  }
  return {
    shell: "flex h-full w-full flex-col bg-card",
    body: "min-h-0 flex-1 overflow-auto",
  };
}

export function FeedbackPanel({ target, canModerate, onClose, flow }: Props) {
  const shape = panelShape(flow);
  const filter = filterForTarget(target);
  const { data, isLoading, hasNextPage, isFetchingNextPage, fetchNextPage } =
    useInfiniteThreads(filter);
  const { availableAnchor } = useTextQuoteAnchor();
  const [view, setView] = useState<View>({ kind: "list" });

  const threads = data?.data ?? [];
  const openCount = threads.filter((t) => t.status === "open").length;
  const needsResolution = threads.filter(
    (t) => t.requires_resolution && t.status !== "resolved" && t.status !== "wont_fix",
  ).length;

  return (
    <div className={shape.shell}>
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
          <Button
            type="button"
            size="sm"
            onClick={() => setView({ kind: "new" })}
            className="ml-auto text-xs"
          >
            <Plus /> New
          </Button>
        )}
        {onClose && (
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            onClick={onClose}
            className={view.kind === "list" ? undefined : "ml-auto"}
            aria-label="Close feedback"
          >
            <X />
          </Button>
        )}
      </div>

      {/* Body */}
      <div className={shape.body}>
        {view.kind === "list" && (
          <>
            <ThreadList
              threads={threads}
              isLoading={isLoading}
              onSelect={(threadId) => setView({ kind: "detail", threadId })}
            />
            <div className="p-3">
              <InfiniteFooter
                hasMore={hasNextPage}
                isLoadingMore={isFetchingNextPage}
                onLoadMore={fetchNextPage}
              />
            </div>
          </>
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
