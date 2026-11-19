import type { ReactNode } from "react";
import * as DialogPrimitive from "@radix-ui/react-dialog";
import { XIcon } from "lucide-react";

/**
 * BottomSheet is a dialog that rises from the bottom of a phone's screen: a
 * title, a body that scrolls on its own, and a footer that stays put (#2723).
 * It is the Radix dialog the portal's modals use, so focus is trapped and
 * Escape closes it.
 */
export function BottomSheet({
  open,
  onOpenChange,
  title,
  children,
  footer,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  children: ReactNode;
  footer?: ReactNode;
}) {
  return (
    <DialogPrimitive.Root open={open} onOpenChange={onOpenChange}>
      <DialogPrimitive.Portal>
        <DialogPrimitive.Overlay className="fixed inset-0 z-50 bg-black/45" />
        <DialogPrimitive.Content
          aria-describedby={undefined}
          className="fixed inset-x-0 bottom-0 z-50 flex max-h-[85dvh] flex-col rounded-t-2xl border-t bg-card shadow-lg outline-none"
        >
          <div className="flex justify-center pt-2" aria-hidden="true">
            <span className="h-1 w-10 rounded-full bg-muted-foreground/30" />
          </div>
          <div className="flex items-center justify-between py-1 pr-2 pl-4">
            <DialogPrimitive.Title className="text-base font-semibold">{title}</DialogPrimitive.Title>
            <DialogPrimitive.Close
              className="flex size-11 items-center justify-center rounded-md text-muted-foreground hover:text-foreground"
              aria-label={`Close ${title.toLowerCase()}`}
            >
              <XIcon className="size-5" />
            </DialogPrimitive.Close>
          </div>
          <div className="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto px-4 pt-2 pb-4">{children}</div>
          {footer && <div className="flex gap-2 border-t px-4 pt-3 pb-6">{footer}</div>}
        </DialogPrimitive.Content>
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
  );
}
