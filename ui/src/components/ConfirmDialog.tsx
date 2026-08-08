import { ReactNode, useState } from "react";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { AlertCircle, AlertTriangle } from "lucide-react";

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
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        className="max-w-md"
        // No corner dismiss: every other way out of this dialog is refused
        // while a confirm is in flight, and Radix's Close would not be, so an
        // operator could abandon the dialog mid-mutation through that one gap.
        showCloseButton={false}
        aria-describedby={description ? "confirm-description" : undefined}
        onEscapeKeyDown={blockCloseWhileBusy}
        onPointerDownOutside={blockCloseWhileBusy}
        onInteractOutside={blockCloseWhileBusy}
      >
        <DialogHeader className="flex-row items-start gap-3 text-left sm:text-left">
          {destructive && (
            <AlertTriangle
              className="mt-0.5 h-5 w-5 shrink-0 text-destructive"
              aria-hidden
            />
          )}
          <div className="min-w-0 flex-1">
            <DialogTitle>{title}</DialogTitle>
            {description && (
              <DialogDescription id="confirm-description" className="mt-1">
                {description}
              </DialogDescription>
            )}
          </div>
        </DialogHeader>

        {error && (
          <Alert variant="destructive">
            <AlertCircle />
            <AlertDescription className="break-words">{error}</AlertDescription>
          </Alert>
        )}

        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            onClick={() => onOpenChange(false)}
            disabled={inFlight}
          >
            {cancelLabel}
          </Button>
          <Button
            type="button"
            variant={destructive ? "destructive" : "default"}
            onClick={handleConfirm}
            disabled={inFlight}
          >
            {inFlight ? "Working…" : confirmLabel}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
