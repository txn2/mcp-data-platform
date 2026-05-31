import { useLayoutEffect, useRef, useState } from "react";
import { MarkdownRenderer } from "./MarkdownRenderer";

// COLLAPSED_MAX_PX is the clamped height when content overflows, roughly
// three to four lines of body text. Long memory records (which can run to
// hundreds of lines) collapse to this and reveal on demand instead of
// dominating the page (issue #515).
const COLLAPSED_MAX_PX = 84;

// OVERFLOW_SLACK_PX avoids showing a "Show more" toggle for content that
// only barely exceeds the clamp, where expanding reveals almost nothing.
const OVERFLOW_SLACK_PX = 8;

interface CollapsibleMarkdownProps {
  content: string;
  // fadeFrom is the Tailwind gradient color matching the container
  // background so the fade at the clamp edge blends in (e.g. "from-card",
  // "from-muted"). Defaults to the card background.
  fadeFrom?: string;
}

// CollapsibleMarkdown renders markdown clamped to a few lines with a
// Show more / Show less toggle. Content that fits within the clamp renders
// in full with no toggle.
export function CollapsibleMarkdown({
  content,
  fadeFrom = "from-card",
}: CollapsibleMarkdownProps) {
  const ref = useRef<HTMLDivElement>(null);
  const [expanded, setExpanded] = useState(false);
  const [overflowing, setOverflowing] = useState(false);

  // Measure in a layout effect so the clamp is applied before the browser
  // paints. With a plain effect a long record flashes in full for one frame
  // before collapsing. Reset to collapsed when the content changes so an
  // instance reused for a different record (e.g. a detail panel the user
  // clicks through) starts collapsed rather than inheriting the prior
  // record's expanded state.
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) {
      return;
    }
    setOverflowing(el.scrollHeight > COLLAPSED_MAX_PX + OVERFLOW_SLACK_PX);
    setExpanded(false);
  }, [content]);

  const clamped = overflowing && !expanded;

  return (
    <div>
      <div
        ref={ref}
        className="relative overflow-hidden"
        style={clamped ? { maxHeight: COLLAPSED_MAX_PX } : undefined}
      >
        <MarkdownRenderer content={content} bare />
        {clamped && (
          <div
            className={`pointer-events-none absolute inset-x-0 bottom-0 h-8 bg-gradient-to-t ${fadeFrom} to-transparent`}
          />
        )}
      </div>
      {overflowing && (
        <button
          type="button"
          onClick={() => setExpanded((v) => !v)}
          className="mt-1 text-xs font-medium text-primary hover:underline"
        >
          {expanded ? "Show less" : "Show more"}
        </button>
      )}
    </div>
  );
}
