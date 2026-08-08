import { X } from "lucide-react";

import { Button } from "@/components/ui/button";
import { useEscapeToClose } from "@/hooks/useEscapeToClose";
import { cn } from "@/lib/utils";

// DrawerShell is the one shape a right-hand detail slide-over takes: the page
// dimmed behind it, what is being shown plus the way out along the top, the
// detail scrolling under that, and an optional pinned footer for the action the
// drawer exists to offer. Escape closes it, so a reader who opened it from a
// table row is never trapped in it with only a mouse -- through the same hook
// the modal shells use, which is what keeps a confirmation opened inside a
// drawer from dismissing the drawer along with itself.
export function DrawerShell({
  title,
  onClose,
  // The action the drawer leads to, pinned below the scrolling detail.
  footer,
  // busy refuses both dismissals while the drawer's own action is in flight.
  busy = false,
  className,
  children,
}: {
  title: string;
  onClose: () => void;
  footer?: React.ReactNode;
  busy?: boolean;
  className?: string;
  children: React.ReactNode;
}) {
  useEscapeToClose(onClose, busy);

  return (
    <div className="fixed inset-0 z-50 flex justify-end">
      <div
        className="absolute inset-0 bg-black/50"
        onClick={busy ? undefined : onClose}
      />
      <div
        role="dialog"
        aria-modal="true"
        aria-label={title}
        className={cn(
          "relative flex w-full max-w-lg flex-col border-l bg-card shadow-xl",
          className,
        )}
      >
        <div className="flex items-center justify-between gap-2 border-b px-4 py-3">
          <h2 className="truncate text-base font-semibold">{title}</h2>
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label="Close"
            onClick={onClose}
          >
            <X />
          </Button>
        </div>
        <div className="flex-1 space-y-4 overflow-auto p-4">{children}</div>
        {footer && <div className="border-t p-4">{footer}</div>}
      </div>
    </div>
  );
}
