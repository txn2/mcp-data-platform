import { useId, useState } from "react";
import { Lightbulb } from "lucide-react";
import { useCaptureThreadInsight } from "@/api/portal/hooks";
import type { ThreadEvent } from "@/api/portal/types";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import { cn } from "@/lib/utils";
import { checkInsightText, defaultInsightContent, insightBytes, INSIGHT_MAX_BYTES } from "./captureContent";

interface Props {
  threadId: string;
  title?: string;
  events: ThreadEvent[] | undefined;
  /** loading holds the button until the panel knows how long the text is. */
  loading: boolean;
}

// CaptureInsightPanel promotes actionable feedback into the review queue: a
// pending insight an agent can apply as durable knowledge, with this thread
// resolving against it. The caller decides who sees it — it is offered only for
// unlinked correction and suggestion threads, to apply_knowledge holders, which
// mirrors the backend gate.
//
// Text over the server's limit is never cut (#2546): a cut-off insight could
// read as complete in the review queue, so the reviewer shortens it here.
export function CaptureInsightPanel({ threadId, title, events, loading }: Props) {
  const capture = useCaptureThreadInsight();
  const fallback = defaultInsightContent(title, events);
  const [edited, setEdited] = useState<string | null>(null);
  const hasField = insightBytes(fallback) > INSIGHT_MAX_BYTES;
  const text = edited ?? fallback;
  const check = checkInsightText(text);
  const blocked = loading || capture.isPending || (hasField && !check.ok);

  const submit = () => capture.mutate(hasField ? { threadId, content: text } : { threadId });

  return (
    <div className="border-b bg-primary/5 p-3">
      <p className="mb-2 text-xs text-muted-foreground">
        Promote this feedback into the review queue. It becomes a pending insight
        an agent can apply as durable knowledge, and this thread resolves with a
        link to it.
      </p>
      {hasField && <InsightTextField value={text} onChange={setEdited} disabled={capture.isPending} />}
      <Button type="button" size="sm" onClick={submit} disabled={blocked} className="text-xs">
        <Lightbulb />
        {capture.isPending ? "Capturing…" : "Capture as insight"}
      </Button>
      {capture.isError && <p role="alert" className="mt-1.5 text-xs text-destructive">{capture.error.message}</p>}
    </div>
  );
}

function InsightTextField({
  value,
  onChange,
  disabled,
}: {
  value: string;
  onChange: (text: string) => void;
  disabled: boolean;
}) {
  const id = useId();
  const hintId = useId();
  const check = checkInsightText(value);
  const tone = (bad: boolean) => (bad ? "text-destructive" : "text-muted-foreground");
  return (
    <div className="mb-2 flex flex-col gap-1">
      <label htmlFor={id} className="text-xs font-medium">
        Insight text
      </label>
      <Textarea
        id={id}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        disabled={disabled}
        aria-invalid={check.bad}
        aria-describedby={hintId}
        className={cn("h-[232px] resize-y text-xs md:text-xs", check.bad && "border-destructive")}
      />
      <div className="flex justify-between gap-2 text-xs">
        <span id={hintId} className={tone(check.bad)}>{check.hint}</span>
        <span className={cn("whitespace-nowrap", tone(check.over))}>{check.count}</span>
      </div>
    </div>
  );
}
