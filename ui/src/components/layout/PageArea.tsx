import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { cn } from "@/lib/utils";

/**
 * ItemPanelSlot is where an item's feedback panel goes (#2583): the element
 * beside the page, or under it on a narrow screen. Null outside the portal's
 * shell.
 */
export const ItemPanelSlot = createContext<HTMLElement | null>(null);

/** SheetSize is how much of a narrow screen the panel takes (#2705). */
export type SheetSize = "default" | "full";

/**
 * PanelSheetState is the size of the panel while it is a sheet under the page:
 * 60% of the screen by default, or the whole screen below the top bar. Null
 * outside the portal's shell, where a panel is never a sheet.
 */
export interface PanelSheetState {
  size: SheetSize;
  setSize: (size: SheetSize) => void;
}

export const PanelSheet = createContext<PanelSheetState | null>(null);

/** The width from which the panel sits beside the page rather than under it. */
export const BESIDE_THE_PAGE_MIN_WIDTH = 1024;

const SHEET_SIZE_KEY = "feedback-sheet-size";

// The chosen size is a per-viewer convenience: it stays while moving between
// threads and items, and a browser that blocks storage keeps it for the visit.
function readSheetSize(): SheetSize {
  try {
    return localStorage.getItem(SHEET_SIZE_KEY) === "full" ? "full" : "default";
  } catch {
    return "default";
  }
}

function writeSheetSize(size: SheetSize) {
  try {
    localStorage.setItem(SHEET_SIZE_KEY, size);
  } catch {
    // Storage blocked: the size lasts as long as the page.
  }
}

/** useNarrow reports whether the panel is a sheet under the page. */
function useNarrow(): boolean {
  const [narrow, setNarrow] = useState(
    () => typeof window !== "undefined" && window.innerWidth < BESIDE_THE_PAGE_MIN_WIDTH,
  );
  useEffect(() => {
    if (typeof window.matchMedia !== "function") return;
    const mq = window.matchMedia(`(max-width: ${BESIDE_THE_PAGE_MIN_WIDTH - 1}px)`);
    const handler = (e: MediaQueryListEvent) => setNarrow(e.matches);
    mq.addEventListener("change", handler);
    return () => mq.removeEventListener("change", handler);
  }, []);
  return narrow;
}

/**
 * usePanelSheet is the panel's sheet, when it is one: its size and the way to
 * change it. Null beside the page (lg and up) and outside the shell, where the
 * panel keeps its column and its composer as they are.
 */
export function usePanelSheet(): PanelSheetState | null {
  const sheet = useContext(PanelSheet);
  const narrow = useNarrow();
  return narrow ? sheet : null;
}

/**
 * useKeyboardHeight is the height left above an on-screen keyboard, or null
 * while none is open: a sheet is held to it so the reply being typed and the
 * Reply button stay on screen (#2705).
 */
function useKeyboardHeight(): number | null {
  const [height, setHeight] = useState<number | null>(null);
  useEffect(() => {
    const vv = typeof window !== "undefined" ? window.visualViewport : null;
    if (!vv) return;
    const read = () => setHeight(window.innerHeight - vv.height > 120 ? Math.round(vv.height) : null);
    vv.addEventListener("resize", read);
    return () => vv.removeEventListener("resize", read);
  }, []);
  return height;
}

/**
 * PageArea is the page's scrolling surface and the space an item's feedback
 * panel takes beside it. The panel is a sibling of the page rather than a layer
 * over it, so the page narrows and nothing of it is covered.
 *
 * On a narrow screen the panel is a sheet under the page (#2705): 60% of the
 * screen, or, made full size, the whole area below the top bar, laid over the
 * page so the item underneath keeps its place and its state.
 *
 * The page surface is `--background` itself, not a wash of `--muted` over it:
 * cards read as raised against the page only if the page is the ramp's own
 * middle step, and a muted wash would land on top of the fills (tab tracks,
 * code blocks) that also derive from muted.
 */
export function PageArea({ children }: { children: ReactNode }) {
  const [slot, setSlot] = useState<HTMLElement | null>(null);
  const [size, setSizeState] = useState<SheetSize>(readSheetSize);
  const setSize = useCallback((next: SheetSize) => {
    setSizeState(next);
    writeSheetSize(next);
  }, []);
  const sheet = useMemo(() => ({ size, setSize }), [size, setSize]);
  const keyboard = useKeyboardHeight();
  return (
    <ItemPanelSlot.Provider value={slot}>
      <PanelSheet.Provider value={sheet}>
        <div className="group/page relative flex min-h-0 flex-1 flex-col lg:flex-row">
          <main className="min-h-0 min-w-0 flex-1 overflow-auto bg-background p-3 sm:p-6">{children}</main>
          <div
            ref={setSlot}
            data-item-panel=""
            data-sheet-size={size}
            style={keyboard ? { maxHeight: keyboard } : undefined}
            className={cn(
              "flex-none overflow-hidden rounded-t-xl border-t bg-card shadow-[0_-6px_20px_rgb(0_0_0/0.12)] empty:hidden lg:static lg:h-auto lg:w-md lg:rounded-none lg:border-t-0 lg:border-l lg:shadow-none",
              size === "full" ? "absolute inset-0 z-20 h-auto" : "h-[60dvh]",
            )}
          />
        </div>
      </PanelSheet.Provider>
    </ItemPanelSlot.Provider>
  );
}
