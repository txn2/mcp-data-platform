import { useId } from "react";
import { ArrowLeft, MoreHorizontal, Quote, Trash2, X } from "lucide-react";
import type { Thread, ThreadVisibility } from "@/api/portal/types";
import type { PanelSheetState } from "@/components/layout/PageArea";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { SheetSizeButton } from "../SheetControls";
import { ThreadKindBadge, ThreadStatusBadge, ThreadVisibilityBadge, VisibilityChoice } from "../ThreadBadges";
import { useUpdateThread } from "@/api/portal/hooks";
import { usePortalAccess } from "@/hooks/usePortalAccess";
import { formatRelative } from "../meta";

interface HeaderProps {
  thread: Thread;
  mayModerate: boolean;
  deleting: boolean;
  onBack: () => void;
  onDelete: () => void;
  /** statusControl replaces the status badge for a moderator (#2532). */
  statusControl?: React.ReactNode;
  /** sheet makes the header the panel's one row on a narrow screen (#2705). */
  sheet?: PanelSheetState | null;
  onClose?: () => void;
}

// useVisibility is whether the reader is offered who sees the thread, and the
// way to change it. Whoever moderates the thread changes who sees it; an
// external person cannot (#2227).
function useVisibility(thread: Thread, mayModerate: boolean) {
  const update = useUpdateThread();
  const { external } = usePortalAccess();
  return {
    offered: mayModerate && !external,
    pending: update.isPending,
    set: (visibility: ThreadVisibility) => update.mutate({ id: thread.id, visibility }),
  };
}

// ThreadHeader is the way back out, what the thread is, and, for a moderator,
// the way to remove it. Beside the page the Internal / Shared choice has a row
// of its own so the header fits the 448px drawer (#2580); on a phone's sheet
// the header is one row and both move into More (#2705).
export function ThreadHeader(props: HeaderProps) {
  return props.sheet ? <SheetThreadRow {...props} sheet={props.sheet} /> : <ColumnThreadHeader {...props} />;
}

// ThreadIdentity is the way back, the thread's kind, its status and, to whom
// visibility is not offered, who sees it.
function ThreadIdentity({ thread, onBack, statusControl, showVisibility }: {
  thread: Thread;
  onBack: () => void;
  statusControl?: React.ReactNode;
  showVisibility: boolean;
}) {
  return (
    <>
      <Button variant="ghost" size="icon-sm" onClick={onBack} aria-label="Back to list">
        <ArrowLeft />
      </Button>
      <ThreadKindBadge kind={thread.kind} />
      {statusControl ?? <ThreadStatusBadge status={thread.status} />}
      {showVisibility && <ThreadVisibilityBadge visibility={thread.visibility} />}
    </>
  );
}

function SheetThreadRow({ thread, mayModerate, deleting, onBack, onDelete, statusControl, sheet, onClose }: HeaderProps & { sheet: PanelSheetState }) {
  const visibility = useVisibility(thread, mayModerate);
  return (
    <div className="flex flex-none items-center gap-1 border-b px-1 pb-1.5" data-testid="thread-sheet-row">
      <ThreadIdentity thread={thread} onBack={onBack} statusControl={statusControl} showVisibility={!visibility.offered} />
      <div className="ml-auto flex items-center">
        {mayModerate && (
          <MoreMenu
            visibility={visibility.offered ? (thread.visibility ?? "internal") : undefined}
            busy={visibility.pending || deleting}
            onVisibility={visibility.set}
            onDelete={onDelete}
          />
        )}
        <SheetSizeButton sheet={sheet} />
        {onClose && (
          <Button type="button" variant="ghost" size="icon-sm" onClick={onClose} aria-label="Close feedback">
            <X />
          </Button>
        )}
      </div>
    </div>
  );
}

function ColumnThreadHeader({ thread, mayModerate, deleting, onBack, onDelete, statusControl }: HeaderProps) {
  const visibility = useVisibility(thread, mayModerate);
  const labelId = useId();
  return (
    <div className="border-b p-3">
      <div className="flex flex-wrap items-center gap-2">
        <ThreadIdentity thread={thread} onBack={onBack} statusControl={statusControl} showVisibility={!visibility.offered} />
        {mayModerate && (
          <Button
            variant="ghost"
            size="icon-sm"
            onClick={onDelete}
            disabled={deleting}
            className="ml-auto text-destructive hover:bg-destructive/10 hover:text-destructive"
            aria-label="Delete thread"
            title="Delete"
          >
            <Trash2 />
          </Button>
        )}
      </div>
      {visibility.offered && (
        <div className="mt-2 flex items-center gap-3 pl-10">
          <span id={labelId} className="text-xs text-muted-foreground">
            Visibility
          </span>
          <VisibilityChoice
            labelledBy={labelId}
            value={thread.visibility ?? "internal"}
            disabled={visibility.pending}
            onChange={visibility.set}
          />
        </div>
      )}
    </div>
  );
}

// MoreMenu holds what a moderator does to a thread on a narrow screen, where
// the row has room for neither (#2705): who sees it, and deleting it.
function MoreMenu({
  visibility,
  busy,
  onVisibility,
  onDelete,
}: {
  /** visibility is offered when set: an external moderator is not offered it. */
  visibility?: ThreadVisibility;
  busy: boolean;
  onVisibility: (v: ThreadVisibility) => void;
  onDelete: () => void;
}) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="icon-sm" aria-label="More for this thread" title="More">
          <MoreHorizontal />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        {visibility && (
          <>
            <DropdownMenuLabel className="text-xs text-muted-foreground">Visibility</DropdownMenuLabel>
            <DropdownMenuRadioGroup value={visibility} onValueChange={(v) => onVisibility(v as ThreadVisibility)}>
              <DropdownMenuRadioItem value="internal" disabled={busy}>Internal</DropdownMenuRadioItem>
              <DropdownMenuRadioItem value="shared" disabled={busy}>Shared</DropdownMenuRadioItem>
            </DropdownMenuRadioGroup>
            <DropdownMenuSeparator />
          </>
        )}
        <DropdownMenuItem variant="destructive" disabled={busy} onSelect={onDelete}>
          <Trash2 /> Delete thread
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

// ThreadSubject is what the thread is about: who opened it and when, and the
// passage it was anchored to if it was raised against one. A thread opened
// before #2513 keeps its stored title as a heading; a newer one has none and
// is read from its first message, the first entry of the timeline below.
export function ThreadSubject({ thread }: { thread: Thread }) {
  return (
    <div className="border-b p-3">
      {thread.title && <h3 className="text-sm font-semibold">{thread.title}</h3>}
      <p className="text-xs text-muted-foreground">
        {thread.author_email} · {formatRelative(thread.created_at)}
        {thread.target_version ? ` · on v${thread.target_version}` : ""}
        {thread.requires_resolution ? " · needs resolution" : ""}
      </p>
      {thread.anchor?.type === "text_quote" && (
        <p className="mt-1 flex items-start gap-1 rounded border-l-2 border-primary/40 bg-muted/40 px-2 py-1 text-xs italic text-muted-foreground">
          <Quote className="mt-0.5 h-3 w-3 shrink-0" />
          <span className="min-w-0 truncate">{thread.anchor.exact}</span>
        </p>
      )}
    </div>
  );
}
