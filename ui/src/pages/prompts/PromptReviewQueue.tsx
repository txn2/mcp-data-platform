import { useState } from "react";
import { Check, X, Globe, Users } from "lucide-react";
import { markdownToPlainText } from "@/lib/markdownText";
import {
  useAdminPrompts,
  useApprovePromptPromotion,
  useRejectPromptPromotion,
} from "@/api/admin/hooks";
import type { Prompt } from "@/api/admin/types";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { FormError } from "./primitives";

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
    <SectionCard
      className="border-amber-500/30 bg-amber-500/5"
      title={
        <span className="flex items-center gap-2">
          Pending promotion requests
          <Badge variant="warning">{pending.length}</Badge>
        </span>
      }
    >
      <ul className="divide-y divide-border/60">
        {pending.map((p: Prompt) => (
          <ReviewRow
            key={p.id}
            prompt={p}
            busy={actingId === p.id}
            error={actionError?.id === p.id ? actionError.message : null}
            onApprove={() => run(approve, p.id)}
            onReject={() => run(reject, p.id)}
          />
        ))}
      </ul>
    </SectionCard>
  );
}

function ReviewRow({
  prompt: p,
  busy,
  error,
  onApprove,
  onReject,
}: {
  prompt: Prompt;
  busy: boolean;
  error: string | null;
  onApprove: () => void;
  onReject: () => void;
}) {
  return (
    <li className="space-y-1 py-2">
      <div className="flex items-start justify-between gap-4">
        <div className="min-w-0">
          <div className="text-sm font-medium break-words">{p.display_name || p.name}</div>
          <div className="mt-0.5 text-xs text-muted-foreground">
            <span>{p.owner_email || "—"}</span> requests promotion to{" "}
            <RequestedScope prompt={p} />
          </div>
          {p.description && (
            <div className="mt-0.5 text-xs break-words text-muted-foreground">
              {markdownToPlainText(p.description)}
            </div>
          )}
        </div>
        <div className="flex shrink-0 gap-2">
          <Button size="sm" onClick={onApprove} disabled={busy}>
            <Check /> {busy ? "Working..." : "Approve"}
          </Button>
          <Button variant="outline" size="sm" onClick={onReject} disabled={busy}>
            <X /> Reject
          </Button>
        </div>
      </div>
      <FormError message={error} />
    </li>
  );
}

// RequestedScope names the scope the owner asked for, with the icon that scope
// carries everywhere else in the admin view.
function RequestedScope({ prompt: p }: { prompt: Prompt }) {
  if (p.requested_scope === "global") {
    return (
      <Badge variant="info">
        <Globe /> Global
      </Badge>
    );
  }
  return (
    <Badge variant="outline" className="text-purple-600 dark:text-purple-300">
      <Users /> Persona: {(p.requested_personas ?? []).join(", ") || "—"}
    </Badge>
  );
}
