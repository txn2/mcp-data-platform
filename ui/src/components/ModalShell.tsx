import type { ReactNode } from "react";
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

/**
 * ModalScroll is the backdrop and centering behavior on their own, for a
 * modal whose child already carries its own panel chrome (border, background,
 * padding). The child grows to its natural height and the backdrop scrolls,
 * which needs no cooperation from the child at all -- the point being that
 * such a modal cannot be fixed by capping a panel it does not own.
 *
 * ModalShell below is the other shape: it owns the panel, so it can cap the
 * height and keep a header and footer in place while only the body scrolls.
 * Both are built from the same geometry, so neither can drift.
 */
export function ModalScroll({
  onClose,
  width = "max-w-lg",
  label,
  children,
}: {
  onClose: () => void;
  width?: string;
  label?: string;
  children: ReactNode;
}) {
  return (
    <div className={modalOverlayClass} onClick={onClose}>
      <div className={modalRowClass}>
        <div
          role="dialog"
          aria-modal="true"
          aria-label={label}
          className={modalNaturalClass(width)}
          onClick={(e) => e.stopPropagation()}
        >
          {children}
        </div>
      </div>
    </div>
  );
}

interface Props {
  /** onClose fires on a backdrop click. */
  onClose: () => void;
  /** width is the Tailwind max-width class, e.g. "max-w-lg". */
  width?: string;
  /**
   * header and footer stay fixed while the body scrolls. Omit both and the
   * whole of children scrolls, which is right for a modal that is one block
   * of content.
   */
  header?: ReactNode;
  footer?: ReactNode;
  /** bodyClass adds padding or layout to the scrolling region. */
  bodyClass?: string;
  /** label names the dialog for assistive technology. */
  label?: string;
  children: ReactNode;
}

/**
 * ModalShell is the centered-modal container for the portal's hand-rolled
 * overlays -- the ones not built on Radix Dialog. It owns the backdrop, the
 * scroll behavior, and the height cap, so a modal only has to describe its
 * content.
 *
 * It is deliberately not a Radix wrapper: a dialog that wants the focus trap,
 * Escape handling and ARIA wiring uses `components/ui/dialog.tsx`, which takes
 * the same geometry from `lib/modal`.
 */
export function ModalShell({
  onClose,
  width = "max-w-lg",
  header,
  footer,
  bodyClass,
  label,
  children,
}: Props) {
  return (
    <div className={modalOverlayClass} onClick={onClose}>
      <div className={modalRowClass}>
        <div
          role="dialog"
          aria-modal="true"
          aria-label={label}
          className={modalPanelClass(width)}
          // The backdrop closes on click; the panel must not, or every click
          // inside the modal would dismiss it.
          onClick={(e) => e.stopPropagation()}
        >
          {header}
          <div className={cn("min-h-0 flex-1 overflow-y-auto", bodyClass)}>
            {children}
          </div>
          {footer}
        </div>
      </div>
    </div>
  );
}
