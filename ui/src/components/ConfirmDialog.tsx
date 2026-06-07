import { ReactNode, useState } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { AlertCircle, AlertTriangle } from "lucide-react";
import { cn } from "@/lib/utils";

export interface ConfirmDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  description?: ReactNode;
  confirmLabel?: string;
  cancelLabel?: string;
  destructive?: boolean;
  loading?: boolean;
  error?: ReactNode;
  onConfirm: () => void | Promise<void>;
}

// ConfirmDialog replaces window.confirm with a real Radix-backed modal.
// Use destructive=true for delete-style actions to surface the
// warning icon and a red confirm button.
//
// Contract: onConfirm may be async. The dialog disables Escape and
// overlay-click while a confirm is in flight (either driven by the
// caller's `loading` prop or by the dialog's own internal busy
// state), so an operator can't dismiss the dialog mid-mutation.
// Failures thrown from onConfirm are caught and logged; callers
// surface the message via the `error` prop which renders inline
// above the action buttons so the operator sees it while the
// dialog is still open (a parent-banner error would be invisible
// behind the Radix overlay).
export function ConfirmDialog({
  open,
  onOpenChange,
  title,
  description,
  confirmLabel = "Confirm",
  cancelLabel = "Cancel",
  destructive = false,
  loading = false,
  error,
  onConfirm,
}: ConfirmDialogProps) {
  const [busy, setBusy] = useState(false);
  const inFlight = busy || loading;

  const handleConfirm = async () => {
    if (inFlight) return;
    setBusy(true);
    try {
      await onConfirm();
    } catch (err) {
      // Caller owns error surfacing; log so the rejection isn't silent.
      console.error("ConfirmDialog onConfirm rejected:", err);
    } finally {
      setBusy(false);
    }
  };

  const blockCloseWhileBusy = (e: Event) => {
    if (inFlight) e.preventDefault();
  };

  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-50 bg-black/50" />
        <Dialog.Content
          className="fixed left-1/2 top-1/2 z-50 w-full max-w-md -translate-x-1/2 -translate-y-1/2 rounded-md border bg-card p-5 shadow-lg focus:outline-none"
          aria-describedby={description ? "confirm-description" : undefined}
          onEscapeKeyDown={blockCloseWhileBusy}
          onPointerDownOutside={blockCloseWhileBusy}
          onInteractOutside={blockCloseWhileBusy}
        >
          <div className="flex items-start gap-3">
            {destructive && (
              <AlertTriangle className="mt-0.5 h-5 w-5 shrink-0 text-destructive" aria-hidden />
            )}
            <div className="min-w-0 flex-1">
              <Dialog.Title className="text-base font-semibold">{title}</Dialog.Title>
              {description && (
                <Dialog.Description
                  id="confirm-description"
                  className="mt-1 text-sm text-muted-foreground"
                >
                  {description}
                </Dialog.Description>
              )}
            </div>
          </div>
          {error && (
            <div className="mt-4 flex items-start gap-2 rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive">
              <AlertCircle className="mt-0.5 h-4 w-4 shrink-0" />
              <div className="min-w-0 flex-1 break-words">{error}</div>
            </div>
          )}

          <div className="mt-5 flex justify-end gap-2">
            <button
              type="button"
              onClick={() => onOpenChange(false)}
              disabled={inFlight}
              className="rounded-md border bg-background px-3 py-1.5 text-sm hover:bg-muted disabled:opacity-50"
            >
              {cancelLabel}
            </button>
            <button
              type="button"
              onClick={handleConfirm}
              disabled={inFlight}
              className={cn(
                "rounded-md px-3 py-1.5 text-sm font-medium disabled:opacity-50",
                destructive
                  ? "bg-destructive text-destructive-foreground hover:opacity-90"
                  : "bg-primary text-primary-foreground hover:opacity-90",
              )}
            >
              {inFlight ? "Working…" : confirmLabel}
            </button>
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
