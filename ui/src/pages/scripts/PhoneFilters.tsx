import { useState, type ReactNode } from "react";
import { SlidersHorizontal } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";

/** PhoneFilters is the Filters button a phone's bar has in place of the
 * selects, opening them in a dialog, with how many narrow the listing. */
export function PhoneFilters({ active, onClear, children }: { active: number; onClear: () => void; children: ReactNode }) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <Button type="button" variant="outline" size="sm" onClick={() => setOpen(true)} className="h-9 shrink-0">
        <SlidersHorizontal />
        Filters
        {active > 0 && <span className="rounded-full bg-primary px-1.5 text-[11px] text-primary-foreground">{active}</span>}
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Filters</DialogTitle>
          </DialogHeader>
          <div className="flex flex-col gap-2 [&_button]:w-full">{children}</div>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClear} disabled={active === 0}>
              Clear
            </Button>
            <Button type="button" onClick={() => setOpen(false)}>
              Done
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
