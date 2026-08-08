import { type ReactNode } from "react";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";

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
//
// It is the capped shape: reference material runs long, and the title has to
// stay in view while the reader scrolls it.
export function HelpDialog({ open, onOpenChange, title, children }: HelpDialogProps) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        capped
        className="max-w-2xl"
        aria-describedby={undefined}
      >
        <DialogHeader className="shrink-0 border-b px-5 py-3 pr-12">
          <DialogTitle>{title}</DialogTitle>
        </DialogHeader>
        <div className="min-h-0 flex-1 overflow-y-auto px-5 py-4 text-sm leading-relaxed">
          {children}
        </div>
      </DialogContent>
    </Dialog>
  );
}
