import { Plus } from "lucide-react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

// The master/detail shell the personas and connections panels share: a fixed
// left rail listing the records, a scrolling detail pane, and one "add" button
// pinned to the bottom of the rail. Both panels select the same way and mark
// the selected row the same way, so the shape is written once here.

export function MasterDetail({
  list,
  children,
}: {
  list: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <div className="flex h-full overflow-hidden">
      {list}
      <div className="flex-1 overflow-auto">{children}</div>
    </div>
  );
}

export function DetailList({
  footer,
  children,
}: {
  footer?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <div className="flex w-56 shrink-0 flex-col overflow-hidden border-r bg-muted/10">
      <div className="flex-1 overflow-auto">{children}</div>
      {footer && <div className="border-t p-2">{footer}</div>}
    </div>
  );
}

// DetailListItem is one selectable record. The selected row carries a primary
// left edge; every row reserves that edge so selecting one does not shift the
// text of the others.
//
// It stays a raw `button` rather than ui/button: this is a full-bleed,
// multi-line navigation row whose selected state is a left edge, and every
// Button variant would have to be unstyled back down to it.
export function DetailListItem({
  selected,
  onClick,
  children,
}: {
  selected: boolean;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "flex w-full flex-col border-b border-l-2 px-4 py-3 text-left transition-colors",
        selected
          ? "border-l-primary bg-primary/5"
          : "border-l-transparent hover:bg-muted/50",
      )}
    >
      {children}
    </button>
  );
}

export function DetailListGroupLabel({ children }: { children: React.ReactNode }) {
  return (
    <div className="border-b bg-muted/30 px-4 py-1.5 text-xs font-semibold uppercase tracking-wider text-muted-foreground">
      {children}
    </div>
  );
}

// DetailListEmpty is the rail's own "nothing configured" line. It is not an
// EmptyState: the rail is 224px wide and already sits beside the detail pane
// that carries the real empty message and the action.
export function DetailListEmpty({ children }: { children: React.ReactNode }) {
  return (
    <p className="px-4 py-8 text-center text-xs text-muted-foreground">{children}</p>
  );
}

export function DetailListAddButton({
  active,
  label,
  onClick,
}: {
  // active marks the rail button while the detail pane holds the create form,
  // so the pane and the button that opened it agree.
  active: boolean;
  label: string;
  onClick: () => void;
}) {
  return (
    <Button
      type="button"
      variant="ghost"
      size="sm"
      onClick={onClick}
      className={cn("w-full", active && "bg-primary/10 text-primary hover:bg-primary/10")}
    >
      <Plus />
      {label}
    </Button>
  );
}
