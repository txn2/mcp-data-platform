import { useState } from "react";
import { MessageSquare } from "lucide-react";
import { useThreads } from "@/api/portal/hooks";
import type { FeedbackTarget } from "@/api/portal/types";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { FeedbackPanel } from "./FeedbackPanel";
import { filterForTarget } from "./targetFilter";

interface Props {
  target: FeedbackTarget;
  // canModerate hints that the caller owns/edits the target, so moderation
  // controls show without a round-trip. The backend still enforces access.
  canModerate?: boolean;
}

// FeedbackButton is the single drop-in entry point a viewer mounts in its
// toolbar. It renders a "Feedback (N)" button and, when toggled, a right-side
// slide-over panel. State is fully self-contained so viewers need no layout
// changes. The slide-over is intentionally non-modal so a text selection in the
// content (used for anchoring) survives opening the panel.
export function FeedbackButton({ target, canModerate = false }: Props) {
  const [open, setOpen] = useState(false);
  const { data } = useThreads(filterForTarget(target));
  const openCount = (data?.data ?? []).filter((t) => t.status === "open").length;

  return (
    <>
      <Button
        type="button"
        variant="outline"
        size="sm"
        onClick={() => setOpen(true)}
        title="Feedback"
      >
        <MessageSquare />
        Feedback
        {openCount > 0 && <Badge className="px-1.5 text-[11px]">{openCount}</Badge>}
      </Button>

      {open && (
        <>
          {/* Non-modal backdrop: dims the page and closes on click, but does not
              trap focus or clear the document selection used for anchoring. */}
          <div
            className="fixed inset-0 z-40 bg-black/20"
            onClick={() => setOpen(false)}
            aria-hidden="true"
          />
          <div className="fixed inset-y-0 right-0 z-50 w-full max-w-md border-l shadow-xl">
            <FeedbackPanel
              target={target}
              canModerate={canModerate}
              onClose={() => setOpen(false)}
            />
          </div>
        </>
      )}
    </>
  );
}
