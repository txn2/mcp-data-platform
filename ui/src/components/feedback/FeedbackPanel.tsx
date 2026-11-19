import { useState } from "react";
import { X, Plus } from "lucide-react";
import { useInfiniteThreads } from "@/api/portal/hooks";
import type { FeedbackTarget } from "@/api/portal/types";
import { InfiniteFooter } from "@/components/InfiniteFooter";
import { Button } from "@/components/ui/button";
import type { PanelSheetState } from "@/components/layout/PageArea";
import { ThreadList } from "./ThreadList";
import { ThreadDetail } from "./ThreadDetail";
import { NewThreadForm } from "./NewThreadForm";
import { GeneralFeedbackNotice } from "./GeneralFeedbackNotice";
import { useTextQuoteAnchor } from "./useTextQuoteAnchor";
import { filterForTarget, targetLabel } from "./targetFilter";
import { SheetHandle, SheetSizeButton } from "./SheetControls";

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
  // sheet is set while the panel is a sheet under the item on a narrow screen
  // (#2705): it carries a handle and a size button, and an open thread's own
  // row replaces the panel's header.
  sheet?: PanelSheetState | null;
}

type View =
  { kind: "list" } | { kind: "new" } | { kind: "detail"; threadId: string };

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

export function FeedbackPanel({ target, canModerate, onClose, flow, sheet }: Props) {
  const shape = panelShape(flow);
  const filter = filterForTarget(target);
  const { data, isLoading, hasNextPage, isFetchingNextPage, fetchNextPage } =
    useInfiniteThreads(filter);
  const { availableAnchor } = useTextQuoteAnchor();
  const [view, setView] = useState<View>({ kind: "list" });

  const threads = data?.data ?? [];
  // On a sheet an open thread's row is the header: the panel's own title and
  // count would take a line the comments need.
  const threadOwnsHeader = sheet && view.kind === "detail";

  return (
    <div className={shape.shell} data-testid="feedback-panel">
      {sheet && <SheetHandle sheet={sheet} />}
      {!threadOwnsHeader && (
        <PanelHeader
          target={target}
          threads={threads}
          sheet={sheet}
          onNew={view.kind === "list" ? () => setView({ kind: "new" }) : undefined}
          onClose={onClose}
        />
      )}

      {/* Body: one scrolling surface, the list, the form, or the thread. */}
      <div className={shape.body} data-testid="feedback-panel-body">
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
          <NewThreadView
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
            sheet={sheet}
            onClose={onClose}
          />
        )}
      </div>
    </div>
  );
}

// PanelHeader is what the panel is about, how many threads are open, New on
// the list, the sheet's size button on a narrow screen, and the close button.
function PanelHeader({
  target,
  threads,
  sheet,
  onNew,
  onClose,
}: {
  target: FeedbackTarget;
  threads: { status: string; requires_resolution: boolean }[];
  sheet?: PanelSheetState | null;
  onNew?: () => void;
  onClose?: () => void;
}) {
  const openCount = threads.filter((t) => t.status === "open").length;
  const needsResolution = threads.filter(
    (t) => t.requires_resolution && t.status !== "resolved" && t.status !== "wont_fix",
  ).length;
  return (
    <div className={sheet ? "flex items-center gap-1 border-b px-3 pb-2" : "flex items-center gap-2 border-b p-3"}>
      <div className="min-w-0">
        <h2 className="truncate text-sm font-semibold">{targetLabel(target)}</h2>
        <p className="text-xs text-muted-foreground">
          {openCount} open
          {needsResolution > 0 ? ` · ${needsResolution} need resolution` : ""}
        </p>
      </div>
      <div className="ml-auto flex items-center gap-1">
        {onNew && (
          <Button type="button" size="sm" onClick={onNew} className="text-xs">
            <Plus /> New
          </Button>
        )}
        {sheet && <SheetSizeButton sheet={sheet} />}
        {onClose && (
          <Button type="button" variant="ghost" size="icon-sm" onClick={onClose} aria-label="Close feedback">
            <X />
          </Button>
        )}
      </div>
    </div>
  );
}

// NewThreadView is the composer, with the line saying where a General post
// goes above it on the standalone channel (#2603).
function NewThreadView(props: React.ComponentProps<typeof NewThreadForm>) {
  return (
    <>
      {props.target.type === "standalone" && (
        <div className="border-b px-4 py-2">
          <GeneralFeedbackNotice />
        </div>
      )}
      <NewThreadForm {...props} />
    </>
  );
}
