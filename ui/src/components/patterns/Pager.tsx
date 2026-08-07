import { ChevronLeft, ChevronRight } from "lucide-react";

import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

// Pager is the one page-through control for a bounded list: the range it is
// showing on the left, previous/next around the page position on the right.
// Every list that pages had been restating the same two disabled-at-the-ends
// buttons; this fixes the wording ("Showing 1-25 of 30"), the labels screen
// readers hear, and the disabled behaviour in one place.
//
// `page` is 1-based and the page count is derived, so a caller only tracks the
// page it is on. Callers render it only when there is more than one page.
export function Pager({
  page,
  perPage,
  total,
  onPage,
  className,
}: {
  page: number;
  perPage: number;
  total: number;
  onPage: (page: number) => void;
  className?: string;
}) {
  const pageCount = Math.max(1, Math.ceil(total / perPage));
  const current = Math.min(Math.max(1, page), pageCount);
  const first = total === 0 ? 0 : (current - 1) * perPage + 1;
  const last = Math.min(current * perPage, total);

  return (
    <div
      className={cn(
        "flex items-center justify-between gap-2 text-xs text-muted-foreground",
        className,
      )}
    >
      <span className="tabular-nums">
        Showing {first}&ndash;{last} of {total}
      </span>
      <div className="flex items-center gap-1">
        <Button
          type="button"
          variant="outline"
          size="xs"
          aria-label="Previous page"
          disabled={current <= 1}
          onClick={() => onPage(current - 1)}
        >
          <ChevronLeft />
          Prev
        </Button>
        <span className="px-1 tabular-nums">
          Page {current} of {pageCount}
        </span>
        <Button
          type="button"
          variant="outline"
          size="xs"
          aria-label="Next page"
          disabled={current >= pageCount}
          onClick={() => onPage(current + 1)}
        >
          Next
          <ChevronRight />
        </Button>
      </div>
    </div>
  );
}
