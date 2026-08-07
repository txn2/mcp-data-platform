import * as Dialog from "@radix-ui/react-dialog";
import { modalNaturalClass, modalOverlayClass, modalRowClass } from "@/components/ModalShell";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";

/**
 * The note that names a new version, asked for at the moment of saving.
 *
 * The summary lives with the editor state rather than in this dialog, so a
 * failed save can reopen it with what the author already wrote; the dialog only
 * shows the field and reports the two ways out.
 */
export function ChangeSummaryDialog({
  open,
  onOpenChange,
  nextVersion,
  value,
  onChange,
  onSave,
  saving,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  nextVersion: number;
  value: string;
  onChange: (v: string) => void;
  onSave: () => void;
  saving: boolean;
}) {
  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Overlay className={modalOverlayClass}>
          <div className={modalRowClass}>
            <Dialog.Content
              className={modalNaturalClass(
                "max-w-sm",
                "space-y-4 rounded-md border bg-card p-5 shadow-lg focus:outline-none",
              )}
              aria-describedby="change-summary-description"
              onEscapeKeyDown={(e) => {
                if (saving) e.preventDefault();
              }}
              onInteractOutside={(e) => {
                if (saving) e.preventDefault();
              }}
            >
              <Dialog.Title className="text-base font-semibold">What changed?</Dialog.Title>
              <Dialog.Description
                id="change-summary-description"
                className="text-xs text-muted-foreground"
              >
                Saving will create a new version v{nextVersion}.
              </Dialog.Description>
              <Textarea
                value={value}
                onChange={(e) => onChange(e.target.value)}
                placeholder="Describe your changes (optional)"
                aria-label="Change summary"
                rows={3}
                // ui/textarea sizes to its content unless asked for a height.
                className="field-sizing-fixed resize-none"
                autoFocus
              />
              <div className="flex justify-end gap-2">
                <Button variant="outline" size="sm" onClick={() => onOpenChange(false)}>
                  Cancel
                </Button>
                <Button size="sm" onClick={onSave} disabled={saving}>
                  {saving ? "Saving..." : "Save"}
                </Button>
              </div>
            </Dialog.Content>
          </div>
        </Dialog.Overlay>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
