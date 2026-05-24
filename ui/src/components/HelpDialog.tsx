import * as Dialog from "@radix-ui/react-dialog";
import { X } from "lucide-react";
import { type ReactNode } from "react";

export interface HelpDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  // Body renders the dialog content. Callers pass a JSX tree (sections,
  // tables, code blocks, etc.) rather than a fixed shape so the same
  // primitive serves every help surface in the app.
  children: ReactNode;
}

// HelpDialog is the read-only sibling of PromptDialog. No form, no
// confirm/cancel pair, just titled content the operator can dismiss.
// Used to keep dense reference material (auth-mode comparison tables,
// mTLS setup walkthroughs, etc.) out of inline form prose so the form
// itself stays scannable.
export function HelpDialog({ open, onOpenChange, title, children }: HelpDialogProps) {
  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-50 bg-black/50" />
        <Dialog.Content
          className="fixed left-1/2 top-1/2 z-50 flex max-h-[85vh] w-full max-w-2xl -translate-x-1/2 -translate-y-1/2 flex-col rounded-md border bg-card shadow-lg focus:outline-none"
          aria-describedby={undefined}
        >
          <div className="flex items-start justify-between gap-4 border-b px-5 py-3">
            <Dialog.Title className="text-base font-semibold">{title}</Dialog.Title>
            <Dialog.Close
              className="rounded-md p-1 text-muted-foreground hover:bg-muted hover:text-foreground"
              aria-label="Close"
            >
              <X className="h-4 w-4" />
            </Dialog.Close>
          </div>
          <div className="overflow-y-auto px-5 py-4 text-sm leading-relaxed">
            {children}
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
