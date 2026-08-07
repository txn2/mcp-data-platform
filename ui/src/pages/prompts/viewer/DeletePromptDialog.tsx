import { AlertTriangle } from "lucide-react";
import type { Prompt } from "@/api/admin/types";
import { ModalScroll } from "@/components/ModalShell";
import { Button } from "@/components/ui/button";

// DeletePromptDialog is the delete-confirmation modal for a prompt. Rendered
// only while open by the parent.
export function DeletePromptDialog({
  prompt,
  pending,
  onCancel,
  onConfirm,
}: {
  prompt: Prompt;
  pending: boolean;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  return (
    <ModalScroll onClose={onCancel} width="max-w-sm" label="Delete prompt">
      <div className="space-y-4 rounded-lg border bg-card p-6 shadow-lg">
        <div className="flex items-center gap-3">
          <div className="flex size-10 shrink-0 items-center justify-center rounded-full bg-destructive/10">
            <AlertTriangle className="size-5 text-destructive" />
          </div>
          <div>
            <h3 className="text-sm font-semibold">Delete prompt</h3>
            <p className="text-sm text-muted-foreground">
              This action cannot be undone.
            </p>
          </div>
        </div>
        <p className="text-sm">
          Delete{" "}
          <span className="font-medium">
            {prompt.display_name || prompt.name}
          </span>
          ?
        </p>
        <div className="flex justify-end gap-2">
          <Button variant="secondary" onClick={onCancel}>
            Cancel
          </Button>
          <Button variant="destructive" onClick={onConfirm} disabled={pending}>
            {pending ? "Deleting..." : "Delete"}
          </Button>
        </div>
      </div>
    </ModalScroll>
  );
}
