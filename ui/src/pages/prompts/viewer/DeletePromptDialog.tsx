import { AlertTriangle } from "lucide-react";
import type { Prompt } from "@/api/admin/types";

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
    <div className="fixed inset-0 z-50 flex items-center justify-center">
      <div
        className="absolute inset-0 bg-black/50"
        onClick={onCancel}
        onKeyDown={(e) => { if (e.key === "Escape") onCancel(); }}
        role="button"
        tabIndex={-1}
        aria-label="Close"
      />
      <div className="relative rounded-lg border bg-card p-6 shadow-lg max-w-sm w-full mx-4 space-y-4">
        <div className="flex items-center gap-3">
          <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-full bg-destructive/10">
            <AlertTriangle className="h-5 w-5 text-destructive" />
          </div>
          <div>
            <h3 className="text-sm font-semibold">Delete prompt</h3>
            <p className="text-sm text-muted-foreground">This action cannot be undone.</p>
          </div>
        </div>
        <p className="text-sm">
          Delete <span className="font-medium">{prompt.display_name || prompt.name}</span>?
        </p>
        <div className="flex justify-end gap-2">
          <button onClick={onCancel} className="rounded-md bg-secondary px-4 py-2 text-sm font-medium text-secondary-foreground hover:bg-secondary/80">Cancel</button>
          <button
            onClick={onConfirm}
            disabled={pending}
            className="rounded-md bg-destructive px-4 py-2 text-sm font-medium text-destructive-foreground hover:bg-destructive/90 disabled:opacity-50"
          >
            {pending ? "Deleting..." : "Delete"}
          </button>
        </div>
      </div>
    </div>
  );
}
