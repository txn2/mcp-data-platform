import { useState, type ReactNode } from "react";
import { SlidersHorizontal, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { BottomSheet } from "./BottomSheet";

/** One filter in force, as a chip that clears it. */
export interface ActiveFilter {
  key: string;
  label: string;
  onClear: () => void;
}

/**
 * PhoneFilterBar is a list's filter bar on a phone (#2723): the scope and the
 * search stay on screen, every other filter folds behind a Filters button that
 * counts the ones in force, and each one in force shows as a chip that clears
 * it. The ordering and the layout toggle stay beside the button.
 */
export function PhoneFilterBar({
  scope,
  search,
  filters,
  active,
  sort,
  view,
  action,
  showLabel = "Done",
}: {
  scope?: ReactNode;
  search: ReactNode;
  /** The controls the sheet holds, each laid out full width. */
  filters: ReactNode;
  active: ActiveFilter[];
  sort?: ReactNode;
  view?: ReactNode;
  /** A page action that stays on screen (New Collection). */
  action?: ReactNode;
  /** The sheet's closing button, "Show 6 assets" when the count is known. */
  showLabel?: string;
}) {
  const [open, setOpen] = useState(false);
  return (
    <div className="flex flex-col gap-2.5" data-testid="phone-filter-bar">
      {scope && (
        <div className="[&_[role=tablist]]:grid [&_[role=tablist]]:h-11 [&_[role=tablist]]:w-full [&_[role=tablist]]:grid-cols-3">
          {scope}
        </div>
      )}
      <div className="[&>*]:w-full">{search}</div>
      <div className="flex items-center gap-2">
        <Button type="button" variant="outline" className="h-11 min-w-0 flex-1" onClick={() => setOpen(true)}>
          <SlidersHorizontal />
          Filters
          {active.length > 0 && (
            <span className="inline-flex size-5 items-center justify-center rounded-full bg-primary text-xs text-primary-foreground">
              {active.length}
            </span>
          )}
        </Button>
        {sort}
        {view}
      </div>
      {active.length > 0 && (
        <div className="flex flex-wrap gap-1.5" aria-label="Filters in force">
          {active.map((f) => (
            <button
              key={f.key}
              type="button"
              onClick={f.onClear}
              className="inline-flex min-h-8 max-w-full items-center gap-1.5 rounded-full border bg-muted/40 px-2.5 text-xs"
            >
              <span className="truncate">{f.label}</span>
              <X className="size-3.5 shrink-0" aria-hidden="true" />
              <span className="sr-only">Clear</span>
            </button>
          ))}
        </div>
      )}
      {action}
      <BottomSheet
        open={open}
        onOpenChange={setOpen}
        title="Filters"
        footer={
          <>
            <Button
              type="button"
              variant="outline"
              className="h-12 flex-1"
              disabled={active.length === 0}
              onClick={() => active.forEach((f) => f.onClear())}
            >
              Clear all
            </Button>
            <Button type="button" className="h-12 flex-[2]" onClick={() => setOpen(false)}>
              {showLabel}
            </Button>
          </>
        }
      >
        <div className="flex flex-col gap-4 [&_button[role=combobox]]:w-full [&>*]:w-full">{filters}</div>
      </BottomSheet>
    </div>
  );
}
