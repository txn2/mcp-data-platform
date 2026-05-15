import { ReactNode, useEffect, useState } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { AlertCircle } from "lucide-react";

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
  // eslint-disable-next-line react-hooks/exhaustive-deps
  useEffect(() => {
    if (!open) return;
    const next: Record<string, string> = {};
    for (const f of fields) next[f.name] = f.defaultValue ?? "";
    setValues(next);
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
      // eslint-disable-next-line no-console
      console.error("PromptDialog onConfirm rejected:", err);
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
          className="fixed left-1/2 top-1/2 z-50 w-full max-w-lg -translate-x-1/2 -translate-y-1/2 rounded-md border bg-card p-5 shadow-lg focus:outline-none"
          aria-describedby={description ? "prompt-description" : undefined}
          onEscapeKeyDown={blockCloseWhileBusy}
          onPointerDownOutside={blockCloseWhileBusy}
          onInteractOutside={blockCloseWhileBusy}
        >
          <Dialog.Title className="text-base font-semibold">{title}</Dialog.Title>
          {description && (
            <Dialog.Description
              id="prompt-description"
              className="mt-1 text-sm text-muted-foreground"
            >
              {description}
            </Dialog.Description>
          )}
          <form onSubmit={handleSubmit} className="mt-4 space-y-4">
            {fields.map((f) => (
              <div key={f.name}>
                <label htmlFor={`prompt-field-${f.name}`} className="mb-1 block text-xs font-medium">
                  {f.label}
                  {f.required && <span className="ml-0.5 text-destructive">*</span>}
                </label>
                <input
                  id={`prompt-field-${f.name}`}
                  type="text"
                  value={values[f.name] ?? ""}
                  onChange={(e) =>
                    setValues((prev) => ({
                      ...prev,
                      [f.name]: f.normalize ? f.normalize(e.target.value) : e.target.value,
                    }))
                  }
                  placeholder={f.placeholder}
                  className={
                    "block w-full rounded-md border bg-background px-2 py-1.5 text-sm focus:outline-none focus:ring-1 focus:ring-ring " +
                    (f.monospace ? "font-mono" : "")
                  }
                />
                {f.help && (
                  <p className="mt-1 text-xs text-muted-foreground">{f.help}</p>
                )}
              </div>
            ))}

            {error && (
              <div className="flex items-start gap-2 rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive">
                <AlertCircle className="mt-0.5 h-4 w-4 shrink-0" />
                <div className="min-w-0 flex-1 break-words">{error}</div>
              </div>
            )}

            <div className="flex justify-end gap-2 pt-1">
              <button
                type="button"
                onClick={() => onOpenChange(false)}
                disabled={inFlight}
                className="rounded-md border bg-background px-3 py-1.5 text-sm hover:bg-muted disabled:opacity-50"
              >
                {cancelLabel}
              </button>
              <button
                type="submit"
                disabled={inFlight || missingRequired}
                className="rounded-md bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground hover:opacity-90 disabled:opacity-50"
              >
                {inFlight ? "Working…" : confirmLabel}
              </button>
            </div>
          </form>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
