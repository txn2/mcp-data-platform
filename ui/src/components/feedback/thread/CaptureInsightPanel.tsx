import { Lightbulb } from "lucide-react";
import { useCaptureThreadInsight } from "@/api/portal/hooks";
import { Button } from "@/components/ui/button";

// CaptureInsightPanel promotes actionable feedback into the review queue: a
// pending insight an agent can apply as durable knowledge, with this thread
// resolving against it. The caller decides who sees it — it is offered only for
// unlinked correction and suggestion threads, to apply_knowledge holders, which
// mirrors the backend gate.
export function CaptureInsightPanel({ threadId }: { threadId: string }) {
  const capture = useCaptureThreadInsight();
  return (
    <div className="border-b bg-primary/5 p-3">
      <p className="mb-2 text-xs text-muted-foreground">
        Promote this feedback into the review queue. It becomes a pending insight
        an agent can apply as durable knowledge, and this thread resolves with a
        link to it.
      </p>
      <Button
        type="button"
        size="sm"
        onClick={() => capture.mutate({ threadId })}
        disabled={capture.isPending}
        className="text-xs"
      >
        <Lightbulb />
        {capture.isPending ? "Capturing…" : "Capture as insight"}
      </Button>
      {capture.isError && (
        <p className="mt-1.5 text-xs text-destructive">
          Could not capture this as an insight.
        </p>
      )}
    </div>
  );
}
