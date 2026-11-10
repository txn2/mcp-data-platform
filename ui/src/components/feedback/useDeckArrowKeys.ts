import { useEffect } from "react";
import { DECK_FRAME_ATTR, deckMoveMessage, type DeckMove } from "@/lib/deck";

const MOVES: Record<string, DeckMove> = {
  ArrowLeft: "left",
  ArrowRight: "right",
  ArrowUp: "up",
  ArrowDown: "down",
};

// What already has a use for an arrow key: text fields, the widgets that move
// their own selection with them, and a dialog open over the page.
const KEEPS_ARROWS = [
  "input",
  "textarea",
  "select",
  '[contenteditable]:not([contenteditable="false"])',
  ...["dialog", "alertdialog", "tab", "radio", "menu", "menuitem", "menuitemradio", "menuitemcheckbox", "option", "listbox", "combobox", "slider"].map(
    (role) => `[role="${role}"]`,
  ),
].join(",");

function keepsArrows(target: EventTarget | null): boolean {
  return target instanceof Element && target.closest(KEEPS_ARROWS) !== null;
}

function isPlainKey(e: KeyboardEvent): boolean {
  return !(e.defaultPrevented || e.altKey || e.ctrlKey || e.metaKey || e.shiftKey || e.isComposing);
}

/**
 * useDeckArrowKeys pages a slide deck with the arrow keys while the reader's
 * focus is on the page around it (#2583). A key pressed inside the deck's frame
 * never reaches this page and the deck handles it itself, so this covers the
 * focus being anywhere else, such as on the feedback panel beside it.
 */
export function useDeckArrowKeys(active: boolean): void {
  useEffect(() => {
    if (!active) return;
    const onKeyDown = (e: KeyboardEvent) => {
      const move = MOVES[e.key];
      if (!move || !isPlainKey(e) || keepsArrows(e.target)) return;
      const deck = document.querySelector<HTMLIFrameElement>(`iframe[${DECK_FRAME_ATTR}]`);
      if (!deck?.contentWindow) return;
      e.preventDefault();
      deck.contentWindow.postMessage(deckMoveMessage(move), "*");
    };
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, [active]);
}
