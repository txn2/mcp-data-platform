import { useState } from "react";
import { CheckCircle2, XCircle } from "lucide-react";
import { useRespondValidation } from "@/api/portal/hooks";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";

// ValidationPanel is the feedback author's answer to a resolution (#603): they
// confirm it, or they dispute it with a reason that re-opens the thread. It is
// rendered only for the author of a thread whose validation is pending, so the
// panel's presence is itself the request.
export function ValidationPanel({ threadId }: { threadId: string }) {
  const respondValidation = useRespondValidation();
  const [disputeReason, setDisputeReason] = useState("");

  const respond = (result: "validated" | "disputed") =>
    respondValidation.mutate({
      threadId,
      result,
      reason: result === "disputed" ? disputeReason.trim() : undefined,
    });

  // Not an `Alert`: that hard-codes `role="alert"`, which would put the reason
  // box and both buttons inside an assertive live region, and its title line is
  // clamped to one line — the request wraps to two in a drawer this narrow.
  return (
    <div className="border-b border-amber-500/30 bg-amber-500/5 p-3">
      <p className="mb-2 text-xs font-medium text-amber-700 dark:text-amber-300">
        Your validation was requested: is this resolved correctly?
      </p>
      <Textarea
        value={disputeReason}
        onChange={(e) => setDisputeReason(e.target.value)}
        rows={2}
        placeholder="Reason (required to dispute)…"
        aria-label="Dispute reason"
        className="field-sizing-fixed min-h-0 w-full resize-y bg-background py-1.5 text-xs"
      />
      <div className="mt-2 flex gap-2">
        <Button
          type="button"
          size="sm"
          onClick={() => respond("validated")}
          disabled={respondValidation.isPending}
          className="bg-emerald-600 text-xs text-white hover:bg-emerald-700"
        >
          <CheckCircle2 /> Validate
        </Button>
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={() => respond("disputed")}
          disabled={respondValidation.isPending || !disputeReason.trim()}
          className="border-destructive/40 text-xs text-destructive hover:bg-destructive/10 hover:text-destructive"
          title={!disputeReason.trim() ? "Add a reason to dispute" : "Dispute and re-open"}
        >
          <XCircle /> Dispute
        </Button>
      </div>
      {respondValidation.isError && (
        <p className="mt-1 text-xs text-destructive">Failed to record your response.</p>
      )}
    </div>
  );
}
