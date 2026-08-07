import { useState } from "react";
import { Info } from "lucide-react";
import { cn } from "@/lib/utils";

// InfoHint puts a view's explanation behind an info toggle instead of an
// always-on paragraph, so the explanation is there on demand without pushing
// the content it explains down the page.
//
// It renders two siblings for one flex-wrap row: the icon button sits inline
// where it is placed, and the open panel is a full-basis, order-last child, so
// it wraps onto its own line below everything else in the row. The parent must
// be flex-wrap for the panel to land below the row.
export function InfoHint({
  label = "About this view",
  children,
}: {
  // The button's accessible name, since its face is only an icon.
  label?: string;
  children: React.ReactNode;
}) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        aria-expanded={open}
        aria-label={label}
        className={cn(
          "rounded-md p-1.5 text-muted-foreground transition-colors hover:bg-muted hover:text-foreground",
          open && "bg-muted text-foreground",
        )}
      >
        <Info className="size-4" />
      </button>
      {open && (
        <div className="order-last w-full basis-full text-sm leading-relaxed text-muted-foreground">
          {children}
        </div>
      )}
    </>
  );
}
