import type { KeyboardEvent, MouseEvent } from "react";

// openRowProps makes a table row open its record (#2587, #2601): a click
// anywhere on the row, or Enter on the focused row, opens it. A link, button
// or field inside the row keeps doing its own thing, so a row's address still
// opens the live site in a new tab without also opening the row.

const INTERACTIVE = "a, button, input, select, textarea, label, [role='button'], [role='menuitem'], [role='checkbox']";

export const openRowClass =
  "group cursor-pointer hover:bg-muted/60 focus-visible:bg-muted/60 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring";

export function openRowProps(open: () => void) {
  return {
    tabIndex: 0,
    className: openRowClass,
    onClick: (e: MouseEvent<HTMLElement>) => {
      const hit = (e.target as HTMLElement).closest(INTERACTIVE);
      if (hit && hit !== e.currentTarget && e.currentTarget.contains(hit)) return;
      open();
    },
    onKeyDown: (e: KeyboardEvent<HTMLElement>) => {
      if (e.key === "Enter" && e.target === e.currentTarget) {
        e.preventDefault();
        open();
      }
    },
  };
}
