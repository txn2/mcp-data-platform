import type { Dispatch, SetStateAction } from "react";
import { Users, Globe, ArrowUpCircle } from "lucide-react";
import { cn } from "@/lib/utils";
import { ModalScroll } from "@/components/ModalShell";

// RequestPromotionDialog is the modal an owner uses to request an admin promote
// their personal prompt to a persona or global scope. Rendered only while open
// by the parent. Extracted verbatim from PromptViewerPage.tsx (#819).
export function RequestPromotionDialog({
  myPersona,
  promoteScope,
  setPromoteScope,
  promoteError,
  pending,
  onCancel,
  onSubmit,
}: {
  myPersona: string;
  promoteScope: "persona" | "global";
  setPromoteScope: Dispatch<SetStateAction<"persona" | "global">>;
  promoteError: string | null;
  pending: boolean;
  onCancel: () => void;
  onSubmit: () => void;
}) {
  return (
    <ModalScroll onClose={onCancel} width="max-w-sm">
      <div className="rounded-lg border bg-card p-6 shadow-lg space-y-4">
        <div>
          <h3 className="text-sm font-semibold">Request promotion</h3>
          <p className="text-sm text-muted-foreground">
            An admin will review and approve. Until then your prompt stays
            personal.
          </p>
        </div>
        <div className="space-y-2">
          <label
            className={cn(
              "flex items-center gap-2 text-sm",
              myPersona ? "cursor-pointer" : "opacity-50 cursor-not-allowed",
            )}
          >
            <input
              type="radio"
              name="promote-scope"
              checked={promoteScope === "persona"}
              disabled={!myPersona}
              onChange={() => setPromoteScope("persona")}
            />
            <Users className="h-4 w-4 text-purple-400" />
            {myPersona ? (
              <>
                My persona{" "}
                <span className="text-muted-foreground">({myPersona})</span>
              </>
            ) : (
              <>
                My persona{" "}
                <span className="text-muted-foreground">
                  (you are not in a persona)
                </span>
              </>
            )}
          </label>
          <label className="flex items-center gap-2 text-sm cursor-pointer">
            <input
              type="radio"
              name="promote-scope"
              checked={promoteScope === "global"}
              onChange={() => setPromoteScope("global")}
            />
            <Globe className="h-4 w-4 text-blue-400" /> Global (everyone)
          </label>
        </div>
        {promoteError && (
          <div className="rounded-md bg-red-500/10 border border-red-500/20 px-3 py-2 text-xs text-red-400">
            {promoteError}
          </div>
        )}
        <div className="flex justify-end gap-2">
          <button
            onClick={onCancel}
            className="rounded-md border px-3 py-1.5 text-sm font-medium hover:bg-accent"
          >
            Cancel
          </button>
          <button
            onClick={onSubmit}
            disabled={pending}
            className="inline-flex items-center gap-1.5 rounded-md bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
          >
            <ArrowUpCircle className="h-3.5 w-3.5" />{" "}
            {pending ? "Submitting..." : "Submit request"}
          </button>
        </div>
      </div>
    </ModalScroll>
  );
}
