import { useState } from "react";
import { Check, X, Globe, Users } from "lucide-react";
import {
  useAdminPrompts,
  useApprovePromptPromotion,
  useRejectPromptPromotion,
} from "@/api/admin/hooks";
import type { Prompt } from "@/api/admin/types";

// PromptReviewQueue surfaces personal prompts whose owners have requested
// promotion to a shared scope (review_requested=true). An admin approves to
// apply the requested scope/personas and mark the prompt approved, or rejects
// to leave it personal. It renders nothing when the queue is empty.
export function PromptReviewQueue() {
  const { data } = useAdminPrompts({ review_requested: true });
  const approve = useApprovePromptPromotion();
  const reject = useRejectPromptPromotion();
  // Track the row currently being acted on so only its buttons disable/show a
  // pending label, and so an error can be attributed to the right request.
  const [actingId, setActingId] = useState<string | null>(null);
  const [actionError, setActionError] = useState<{ id: string; message: string } | null>(null);

  const pending = data?.data ?? [];
  if (pending.length === 0) return null;

  const run = (
    mutation: { mutate: (id: string, opts: { onError: (e: unknown) => void; onSettled: () => void }) => void },
    id: string,
  ) => {
    setActionError(null);
    setActingId(id);
    mutation.mutate(id, {
      onError: (e: unknown) => setActionError({ id, message: e instanceof Error ? e.message : "Action failed" }),
      onSettled: () => setActingId(null),
    });
  };

  return (
    <div className="rounded-lg border border-amber-500/30 bg-amber-500/5 p-4 space-y-3">
      <h3 className="text-sm font-semibold flex items-center gap-2">
        Pending promotion requests
        <span className="rounded-full bg-amber-500/20 px-2 py-0.5 text-xs font-medium text-amber-400">{pending.length}</span>
      </h3>
      <ul className="divide-y divide-border/60">
        {pending.map((p: Prompt) => {
          const busy = actingId === p.id;
          return (
            <li key={p.id} className="py-2 space-y-1">
              <div className="flex items-start justify-between gap-4">
                <div className="min-w-0">
                  <div className="font-medium text-sm break-words">{p.display_name || p.name}</div>
                  <div className="text-xs text-muted-foreground mt-0.5">
                    <span>{p.owner_email || "—"}</span> requests promotion to{" "}
                    {p.requested_scope === "global" ? (
                      <span className="inline-flex items-center gap-1 text-blue-400"><Globe className="h-3 w-3" /> Global</span>
                    ) : (
                      <span className="inline-flex items-center gap-1 text-purple-400">
                        <Users className="h-3 w-3" /> Persona: {(p.requested_personas ?? []).join(", ") || "—"}
                      </span>
                    )}
                  </div>
                  {p.description && (
                    <div className="text-xs text-muted-foreground mt-0.5 break-words">{p.description}</div>
                  )}
                </div>
                <div className="flex gap-2 shrink-0">
                  <button
                    onClick={() => run(approve, p.id)}
                    disabled={busy}
                    className="inline-flex items-center gap-1.5 rounded-md bg-primary px-2.5 py-1 text-xs font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
                  >
                    <Check className="h-3.5 w-3.5" /> {busy ? "Working..." : "Approve"}
                  </button>
                  <button
                    onClick={() => run(reject, p.id)}
                    disabled={busy}
                    className="inline-flex items-center gap-1.5 rounded-md border px-2.5 py-1 text-xs font-medium hover:bg-accent disabled:opacity-50"
                  >
                    <X className="h-3.5 w-3.5" /> Reject
                  </button>
                </div>
              </div>
              {actionError?.id === p.id && (
                <div className="rounded-md bg-red-500/10 border border-red-500/20 px-3 py-2 text-xs text-red-400">{actionError.message}</div>
              )}
            </li>
          );
        })}
      </ul>
    </div>
  );
}
