import { createContext, useState, type ReactNode } from "react";

/**
 * ItemPanelSlot is where an item's feedback panel goes (#2583): the element
 * beside the page, or under it on a narrow screen. Null outside the portal's
 * shell.
 */
export const ItemPanelSlot = createContext<HTMLElement | null>(null);

/**
 * PageArea is the page's scrolling surface and the space an item's feedback
 * panel takes beside it. The panel is a sibling of the page rather than a layer
 * over it, so the page narrows and nothing of it is covered.
 *
 * The page surface is `--background` itself, not a wash of `--muted` over it:
 * cards read as raised against the page only if the page is the ramp's own
 * middle step, and a muted wash would land on top of the fills (tab tracks,
 * code blocks) that also derive from muted.
 */
export function PageArea({ children }: { children: ReactNode }) {
  const [slot, setSlot] = useState<HTMLElement | null>(null);
  return (
    <ItemPanelSlot.Provider value={slot}>
      <div className="group/page flex min-h-0 flex-1 flex-col lg:flex-row">
        <main className="min-h-0 min-w-0 flex-1 overflow-auto bg-background p-3 sm:p-6">{children}</main>
        <div
          ref={setSlot}
          data-item-panel=""
          className="h-[55dvh] flex-none overflow-hidden rounded-t-xl border-t bg-card shadow-[0_-6px_20px_rgb(0_0_0/0.12)] empty:hidden lg:h-auto lg:w-md lg:rounded-none lg:border-t-0 lg:border-l lg:shadow-none"
        />
      </div>
    </ItemPanelSlot.Provider>
  );
}
