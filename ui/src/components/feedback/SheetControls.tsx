import { useRef } from "react";
import { ChevronDown, ChevronUp } from "lucide-react";
import type { PanelSheetState } from "@/components/layout/PageArea";
import { Button } from "@/components/ui/button";

// The feedback panel on a narrow screen is a sheet under the item (#2705). A
// reader makes it bigger, up to the whole screen below the top bar, and back
// down, with the handle across its top or the button in its header.

function sizeLabel(sheet: PanelSheetState): string {
  return sheet.size === "full" ? "Make the feedback sheet smaller" : "Make the feedback sheet bigger";
}

function toggle(sheet: PanelSheetState) {
  sheet.setSize(sheet.size === "full" ? "default" : "full");
}

/**
 * SheetHandle is the bar across the top of the sheet. A tap switches its size;
 * a drag up makes it full size and a drag down makes it smaller again.
 */
export function SheetHandle({ sheet }: { sheet: PanelSheetState }) {
  const startY = useRef<number | null>(null);
  const dragged = useRef(false);
  return (
    <button
      type="button"
      aria-label={sizeLabel(sheet)}
      data-testid="sheet-handle"
      onClick={() => {
        // The click that ends a drag has already set the size.
        if (dragged.current) dragged.current = false;
        else toggle(sheet);
      }}
      onPointerDown={(e) => {
        startY.current = e.clientY;
        dragged.current = false;
      }}
      onPointerUp={(e) => {
        if (startY.current === null) return;
        const moved = e.clientY - startY.current;
        startY.current = null;
        if (Math.abs(moved) < 24) return;
        dragged.current = true;
        sheet.setSize(moved < 0 ? "full" : "default");
      }}
      className="flex h-5 w-full flex-none touch-none items-center justify-center"
    >
      <span aria-hidden className="h-1 w-9 rounded-full bg-muted-foreground/50" />
    </button>
  );
}

/** SheetSizeButton is the same switch as a button in the sheet's header row. */
export function SheetSizeButton({ sheet }: { sheet: PanelSheetState }) {
  const label = sizeLabel(sheet);
  return (
    <Button type="button" variant="ghost" size="icon-sm" onClick={() => toggle(sheet)} aria-label={label} title={label}>
      {sheet.size === "full" ? <ChevronDown /> : <ChevronUp />}
    </Button>
  );
}
