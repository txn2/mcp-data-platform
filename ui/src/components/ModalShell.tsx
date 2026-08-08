import type { ReactNode } from "react";
import { useEscapeToClose } from "@/hooks/useEscapeToClose";
import { cn } from "@/lib/utils";
import {
  modalNaturalClass,
  modalOverlayClass,
  modalPanelClass,
  modalRowClass,
} from "@/lib/modal";

/**
 * The hand-rolled half of the portal's modals: the overlays that are not built
 * on Radix Dialog. They take their geometry from `lib/modal`, which is the same
 * geometry `components/ui/dialog.tsx` gives every Radix dialog, so the two
 * routes cannot drift.
 *
 * Re-exported here because these were this file's exports before the geometry
 * moved below both routes; callers that reach for the classes directly still
 * find them where they always were.
 */
export { modalNaturalClass, modalOverlayClass, modalPanelClass, modalRowClass };

/** The props both shapes share. */
interface CommonProps {
  /** onClose fires on Escape and on a backdrop click. */
  onClose: () => void;
  /** width is the Tailwind max-width class, e.g. "max-w-lg". */
  width?: string;
  /** label names the dialog for assistive technology. */
  label?: string;
  /**
   * busy refuses both dismissals for the span of a mutation the modal has
   * already started, so closing cannot discard the outcome. Pass the same
   * pending flag that disables the submit button.
   */
  busy?: boolean;
  children: ReactNode;
}

interface ShellProps extends CommonProps {
  /**
   * capped bounds the panel at the viewport and lays it out as a column, so
   * `header` and `footer` stay put while only the body scrolls. Without it the
   * panel keeps its natural height and the backdrop scrolls around it.
   */
  capped?: boolean;
  /**
   * header and footer stay fixed while the body scrolls. Omit both and the
   * whole of children scrolls, which is right for a modal that is one block
   * of content.
   */
  header?: ReactNode;
  footer?: ReactNode;
  /** bodyClass adds padding or layout to the scrolling region. */
  bodyClass?: string;
}

/**
 * Shell is the one implementation behind both exported shapes. They differ in
 * the panel class and whether the content sits in a scrolling body, and in
 * nothing else -- so the overlay contract (the backdrop, the two dismissals and
 * the states that refuse them, the dialog role) is written once and cannot be
 * given to one shape and forgotten for the other. `lib/modal` does the same for
 * the geometry, below both this file and the Radix route.
 */
function Shell({
  onClose,
  width = "max-w-lg",
  label,
  busy = false,
  capped = false,
  header,
  footer,
  bodyClass,
  children,
}: ShellProps) {
  useEscapeToClose(onClose, busy);

  return (
    <div
      data-testid="modal-overlay"
      className={modalOverlayClass}
      onClick={busy ? undefined : onClose}
    >
      <div className={modalRowClass}>
        <div
          role="dialog"
          aria-modal="true"
          aria-label={label}
          data-testid="modal-panel"
          className={capped ? modalPanelClass(width) : modalNaturalClass(width)}
          // The backdrop closes on click; the panel must not, or every click
          // inside the modal would dismiss it.
          onClick={(e) => e.stopPropagation()}
        >
          {capped ? (
            <>
              {header}
              <div className={cn("min-h-0 flex-1 overflow-y-auto", bodyClass)}>
                {children}
              </div>
              {footer}
            </>
          ) : (
            children
          )}
        </div>
      </div>
    </div>
  );
}

/**
 * ModalScroll is the backdrop and centering behavior on their own, for a
 * modal whose child already carries its own panel chrome (border, background,
 * padding). The child grows to its natural height and the backdrop scrolls,
 * which needs no cooperation from the child at all -- the point being that
 * such a modal cannot be fixed by capping a panel it does not own.
 *
 * Take this shape only for a modal that is one block of bounded content -- a
 * confirmation, not a detail read whose sections grow with what it is showing.
 * ModalShell below is the other shape: it owns the panel, so it can cap the
 * height and keep a header and footer in place while only the body scrolls.
 */
export function ModalScroll(props: CommonProps) {
  return <Shell {...props} />;
}

/**
 * ModalShell is the centered-modal container for the portal's hand-rolled
 * overlays -- the ones not built on Radix Dialog. It owns the backdrop, the
 * scroll behavior, and the height cap, so a modal only has to describe its
 * content.
 *
 * It is deliberately not a Radix wrapper: a dialog that wants the focus trap
 * and the full ARIA wiring uses `components/ui/dialog.tsx`, which takes the
 * same geometry from `lib/modal`. What it does not leave to Radix is Escape --
 * `hooks/useEscapeToClose` gives every hand-rolled overlay a way out that does
 * not need a pointer, because that is not optional.
 */
export function ModalShell(props: Omit<ShellProps, "capped">) {
  return <Shell {...props} capped />;
}
