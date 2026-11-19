import type { ReactNode } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { modalNaturalClass, modalOverlayClass, modalRowClass } from "@/components/ModalShell";
import { Button } from "@/components/ui/button";

/**
 * The small dialog the status and verification controls ask through: a title,
 * a line under it, its fields, and Cancel beside the action. Pending holds it
 * open, so a click outside cannot drop a write in flight.
 */
export function MarkDialog({
  open,
  onOpenChange,
  title,
  description,
  action,
  onAction,
  pending,
  error,
  children,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  description: string;
  action: string;
  onAction: () => void;
  pending: boolean;
  error?: string | null;
  children?: ReactNode;
}) {
  return (
    <Dialog.Root open={open} onOpenChange={(o) => !pending && onOpenChange(o)}>
      <Dialog.Portal>
        <Dialog.Overlay className={modalOverlayClass}>
          <div className={modalRowClass}>
            <Dialog.Content
              className={modalNaturalClass("max-w-md", "space-y-4 rounded-md border bg-card p-5 shadow-lg focus:outline-none")}
            >
              <Dialog.Title className="text-base font-semibold">{title}</Dialog.Title>
              <Dialog.Description className="text-sm text-muted-foreground">{description}</Dialog.Description>
              {children}
              {error && (
                <p role="alert" className="text-sm text-destructive">
                  {error}
                </p>
              )}
              <div className="flex justify-end gap-2">
                <Button variant="outline" size="sm" onClick={() => onOpenChange(false)} disabled={pending}>
                  Cancel
                </Button>
                <Button size="sm" onClick={onAction} disabled={pending}>
                  {action}
                </Button>
              </div>
            </Dialog.Content>
          </div>
        </Dialog.Overlay>
      </Dialog.Portal>
    </Dialog.Root>
  );
}

/** The message a failed write shows. */
export function errorText(err: unknown): string | null {
  if (!err) return null;
  return err instanceof Error && err.message ? err.message : "The change could not be saved.";
}
