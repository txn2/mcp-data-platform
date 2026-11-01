import type { ReactNode } from "react";
import { Maximize2, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { useEscapeToClose } from "@/hooks/useEscapeToClose";

/**
 * The content of a page shown edge to edge (#2390): on a phone, the header
 * and controls above it take most of a small screen, and an HTML document
 * scrolled inside a frame inside the page. Expanded, the content fills the
 * viewport inside its safe area under one bar that names it and closes it,
 * and is the only thing that scrolls.
 *
 * The same elements are rendered either way and only their classes change,
 * so expanding never reloads a document's frame or loses its place.
 */
export function ExpandedView({
  expanded,
  title,
  onClose,
  children,
}: {
  expanded: boolean;
  title: string;
  onClose: () => void;
  children: ReactNode;
}) {
  return (
    <div
      data-testid="expanded-view"
      data-expanded={expanded || undefined}
      role={expanded ? "dialog" : undefined}
      aria-modal={expanded || undefined}
      aria-label={expanded ? title : undefined}
      className={
        expanded
          ? "fixed inset-0 z-50 flex flex-col bg-background pt-[env(safe-area-inset-top)] pr-[env(safe-area-inset-right)] pb-[env(safe-area-inset-bottom)] pl-[env(safe-area-inset-left)]"
          : "contents"
      }
    >
      {expanded && <ExpandedBar title={title} onClose={onClose} />}
      <div className={expanded ? "flex min-h-0 flex-1 flex-col overflow-auto p-2" : "contents"}>{children}</div>
    </div>
  );
}

function ExpandedBar({ title, onClose }: { title: string; onClose: () => void }) {
  useEscapeToClose(onClose);
  return (
    <div className="flex shrink-0 items-center gap-2 border-b px-3 py-2">
      <span className="min-w-0 flex-1 truncate text-sm font-medium">{title}</span>
      <Button variant="outline" size="sm" onClick={onClose} autoFocus>
        <X />
        Close
      </Button>
    </div>
  );
}

/** The control that opens the Expand view, where it is offered. */
export function ExpandButton({ offered = true, onClick }: { offered?: boolean; onClick: () => void }) {
  if (!offered) return null;
  return (
    <Button variant="outline" size="sm" onClick={onClick} title="Show the content full screen">
      <Maximize2 />
      Expand
    </Button>
  );
}
