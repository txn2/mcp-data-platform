import { ReactNode, useEffect, useState } from "react";
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
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { AlertCircle } from "lucide-react";
import { cn } from "@/lib/utils";

export interface PromptDialogField {
  name: string;
  label: string;
  help?: ReactNode;
  placeholder?: string;
  defaultValue?: string;
  required?: boolean;
  monospace?: boolean;
  normalize?: (raw: string) => string;
}

export interface PromptDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  description?: ReactNode;
  fields: PromptDialogField[];
  confirmLabel?: string;
  cancelLabel?: string;
  loading?: boolean;
  error?: ReactNode;
  onConfirm: (values: Record<string, string>) => void | Promise<void>;
}

// PromptDialog replaces window.prompt with a real form-in-a-modal.
// Supports one or more text fields with optional help text and a
// normalize callback (used by the catalog panel to keep slug input
// in sync with the server validator's character set).
//
// Lifecycle:
//   - Values are seeded from each field's defaultValue exactly once
//     per open transition (depending on the `fields` array
//     identity would clobber typed input on every parent re-render).
//   - Submission disables Escape and overlay-click via Radix's
//     onEscapeKeyDown / onPointerDownOutside hooks so an operator
//     can't dismiss the dialog mid-mutation.
//   - Radix's focus trap handles initial focus on the first
//     focusable child (the first input).
//   - The error prop renders inline above the action buttons so
//     callers can surface server errors without closing the modal.
export function PromptDialog({
  open,
  onOpenChange,
  title,
  description,
  fields,
  confirmLabel = "Save",
  cancelLabel = "Cancel",
  loading = false,
  error,
  onConfirm,
}: PromptDialogProps) {
  const [values, setValues] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const inFlight = busy || loading;

  // Seed values once per open transition. Intentionally NOT depending
  // on `fields` because parents often pass an inline array literal
  // that changes identity every render; depending on it would
  // overwrite the operator's typed input whenever the parent
  // re-renders (TanStack Query refetches, sibling state changes, ...).
  useEffect(() => {
    if (!open) return;
    const next: Record<string, string> = {};
    for (const f of fields) next[f.name] = f.defaultValue ?? "";
    setValues(next);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  const missingRequired = fields.some(
    (f) => f.required && !values[f.name]?.trim(),
  );

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (missingRequired || inFlight) return;
    setBusy(true);
    try {
      await onConfirm(values);
    } catch (err) {
      // Caller owns error surfacing via the `error` prop; log so the
      // rejection isn't silent.
      console.error("PromptDialog onConfirm rejected:", err);
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
        // As in ConfirmDialog: a corner dismiss would be the one exit Radix
        // does not route through the in-flight guards below.
        showCloseButton={false}
        aria-describedby={description ? "prompt-description" : undefined}
        onEscapeKeyDown={blockCloseWhileBusy}
        onPointerDownOutside={blockCloseWhileBusy}
        onInteractOutside={blockCloseWhileBusy}
      >
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          {description && (
            <DialogDescription id="prompt-description">
              {description}
            </DialogDescription>
          )}
        </DialogHeader>

        <form onSubmit={handleSubmit} className="space-y-4">
          {fields.map((f) => (
            <div key={f.name}>
              <Label
                htmlFor={`prompt-field-${f.name}`}
                // gap-0.5 rather than the Label default: the asterisk belongs
                // against the word it qualifies, not a control's width away.
                className="mb-1 gap-0.5 text-xs"
              >
                {f.label}
                {f.required && <span className="text-destructive">*</span>}
              </Label>
              <Input
                id={`prompt-field-${f.name}`}
                type="text"
                value={values[f.name] ?? ""}
                onChange={(e) =>
                  setValues((prev) => ({
                    ...prev,
                    [f.name]: f.normalize
                      ? f.normalize(e.target.value)
                      : e.target.value,
                  }))
                }
                placeholder={f.placeholder}
                className={cn("text-sm", f.monospace && "font-mono")}
              />
              {f.help && (
                <p className="mt-1 text-xs text-muted-foreground">{f.help}</p>
              )}
            </div>
          ))}

          {error && (
            <Alert variant="destructive">
              <AlertCircle />
              <AlertDescription className="break-words">
                {error}
              </AlertDescription>
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
            <Button type="submit" disabled={inFlight || missingRequired}>
              {inFlight ? "Working…" : confirmLabel}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
