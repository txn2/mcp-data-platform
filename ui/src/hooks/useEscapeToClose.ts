import { useEffect } from "react";

/**
 * useEscapeToClose closes a hand-rolled overlay on Escape, so someone who
 * opened one is never held in it with only a pointer. It is the single
 * implementation for every overlay outside Radix -- both `ModalShell` shapes
 * and `DrawerShell` -- because each of the three guards below is a bug when one
 * of them forgets it, and a private copy per overlay is how that happens.
 *
 * It listens in the bubble phase and defers to `defaultPrevented`, which is
 * what makes it safe under a Radix layer: `DismissableLayer` handles Escape on
 * the document in the capture phase and calls `preventDefault()` when it
 * dismisses, so a Select or a nested Dialog opened over the overlay takes the
 * key for itself and the overlay behind it stays open.
 *
 * `busy` detaches the listener entirely, for the span of a mutation the overlay
 * has already started. Closing then would unmount the only component that
 * renders the outcome, so a partial failure reads as success -- which is why
 * the Radix route refuses Escape in the same state (`ConfirmDialog`,
 * `PromptDialog`). Pass the same pending flag that disables the submit button.
 *
 * A keydown raised while an IME is composing is left alone: that Escape
 * cancels the candidate window, and treating it as a dismissal would discard
 * the whole form over one abandoned composition.
 */
export function useEscapeToClose(onClose: () => void, busy = false): void {
  useEffect(() => {
    if (busy) return;

    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key !== "Escape" || e.defaultPrevented || e.isComposing) return;
      onClose();
    };
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, [onClose, busy]);
}
