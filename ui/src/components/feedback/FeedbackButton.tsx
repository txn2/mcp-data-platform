import { useContext, useLayoutEffect, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { MessageSquare } from "lucide-react";
import { useThreads } from "@/api/portal/hooks";
import type { FeedbackTarget } from "@/api/portal/types";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ItemPanelSlot } from "@/components/layout/PageArea";
import { FeedbackPanel } from "./FeedbackPanel";
import { filterForTarget } from "./targetFilter";
import { useDeckArrowKeys } from "./useDeckArrowKeys";
import { usePortalAccess } from "@/hooks/usePortalAccess";

interface Props {
  target: FeedbackTarget;
  // canModerate hints that the caller owns/edits the target, so moderation
  // controls show without a round-trip. The backend still enforces access.
  canModerate?: boolean;
}

// FeedbackButton is the single drop-in entry point a viewer mounts in its
// toolbar. It renders a "Feedback (N)" button and, when toggled, the panel
// beside the item, or under it on a narrow screen. State is fully self-contained
// so viewers need no layout changes. Nothing lies over the item while the panel
// is open (#2583): a reviewer keeps reading, scrolling and paging as they write,
// and a text selection in the content (used for anchoring) survives.
//
// A persona without Feedback (#2225) gets no button, and no thread read: the
// threads routes refuse it.
export function FeedbackButton(props: Props) {
  const { feedback } = usePortalAccess();
  return feedback ? <FeedbackToggle {...props} /> : null;
}

function FeedbackToggle({ target, canModerate = false }: Props) {
  const [open, setOpen] = useState(false);
  const slot = useContext(ItemPanelSlot);
  useDeckArrowKeys(open);
  useLayoutEffect(() => {
    if (open) bringContentAboveSheet();
  }, [open]);
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

      {open && placePanel(<FeedbackPanel target={target} canModerate={canModerate} onClose={() => setOpen(false)} />, slot)}
    </>
  );
}

const BESIDE_THE_PAGE_MIN_WIDTH = 1024;

/**
 * On a narrow screen the panel takes the lower part of the screen, and the
 * page's own title and toolbars would fill what is left. This scrolls them away
 * so the item itself is what shows above the panel; a reader further down the
 * page stays where they are.
 */
function bringContentAboveSheet(): void {
  if (window.innerWidth >= BESIDE_THE_PAGE_MIN_WIDTH) return;
  const main = document.querySelector("main");
  const toolbar = document.querySelector('[data-testid="content-controls"]')?.parentElement;
  if (!main || !toolbar) return;
  const hidden = toolbar.getBoundingClientRect().bottom - main.getBoundingClientRect().top;
  if (hidden > 0) main.scrollTop += hidden;
}

/** The panel in the space beside the page when there is one, in place otherwise. */
function placePanel(panel: ReactNode, slot: HTMLElement | null): ReactNode {
  return slot ? createPortal(panel, slot) : panel;
}
