import { useRef } from "react";
import { Button } from "@/components/ui/button";
import { useInfiniteScroll } from "@/hooks/useInfiniteScroll";

interface Props {
  /** Whether another page can be loaded. */
  hasMore: boolean;
  /** Whether a page fetch is currently in flight. */
  isLoadingMore: boolean;
  /** Load the next page. */
  onLoadMore: () => void;
}

/**
 * InfiniteFooter is the shared "Load more" control for portal list surfaces
 * (#972). It owns the scroll sentinel and wires useInfiniteScroll so the next
 * page auto-loads as the list nears the bottom, and renders a manual "Load more"
 * button as the fallback/indicator (auto-scroll is a no-op in degenerate layouts
 * such as jsdom, so the button is always the reliable path). It renders nothing
 * when there is no further page, so callers can drop it in unconditionally.
 *
 * Progress copy ("Showing X of Y") is intentionally left to each page, which
 * phrases the count per its own scope/filters; this component owns only the
 * mechanism every list shares.
 */
export function InfiniteFooter({ hasMore, isLoadingMore, onLoadMore }: Props) {
  const sentinelRef = useRef<HTMLDivElement>(null);
  useInfiniteScroll(sentinelRef, {
    hasMore,
    isLoading: isLoadingMore,
    onLoadMore,
  });

  if (!hasMore) return null;

  return (
    <>
      {/* Sentinel: observed to auto-load the next page on scroll. */}
      <div ref={sentinelRef} aria-hidden="true" className="h-px" />
      <div className="flex justify-center pt-1">
        <Button type="button" variant="outline" onClick={onLoadMore} disabled={isLoadingMore}>
          {isLoadingMore ? "Loading more…" : "Load more"}
        </Button>
      </div>
    </>
  );
}
