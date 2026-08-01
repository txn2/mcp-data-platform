import { AlertTriangle } from "lucide-react";
import type { Prompt } from "@/api/admin/types";
import { ModalScroll } from "@/components/ModalShell";

// DeletePromptDialog is the delete-confirmation modal for a prompt. Rendered
// only while open by the parent. Extracted verbatim from PromptViewerPage.tsx
// (#819).
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
    <ModalScroll onClose={onCancel} width="max-w-sm">
      <div className="rounded-lg border bg-card p-6 shadow-lg space-y-4">
        <div className="flex items-center gap-3">
          <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-full bg-destructive/10">
            <AlertTriangle className="h-5 w-5 text-destructive" />
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
          <button
            onClick={onCancel}
            className="rounded-md bg-secondary px-4 py-2 text-sm font-medium text-secondary-foreground hover:bg-secondary/80"
          >
            Cancel
          </button>
          <button
            onClick={onConfirm}
            disabled={pending}
            className="rounded-md bg-destructive px-4 py-2 text-sm font-medium text-destructive-foreground hover:bg-destructive/90 disabled:opacity-50"
          >
            {pending ? "Deleting..." : "Delete"}
          </button>
        </div>
      </div>
    </ModalScroll>
  );
}
