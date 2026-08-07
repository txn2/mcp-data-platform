import { useEffect } from "react";
import { X } from "lucide-react";

import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

// DrawerShell is the one shape a right-hand detail slide-over takes: the page
// dimmed behind it, what is being shown plus the way out along the top, the
// detail scrolling under that, and an optional pinned footer for the action the
// drawer exists to offer. Escape closes it, so a reader who opened it from a
// table row is never trapped in it with only a mouse.
export function DrawerShell({
  title,
  onClose,
  // The action the drawer leads to, pinned below the scrolling detail.
  footer,
  className,
  children,
}: {
  title: string;
  onClose: () => void;
  footer?: React.ReactNode;
  className?: string;
  children: React.ReactNode;
}) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  return (
    <div className="fixed inset-0 z-50 flex justify-end">
      <div className="absolute inset-0 bg-black/50" onClick={onClose} />
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
