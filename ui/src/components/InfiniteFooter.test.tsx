import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { InfiniteFooter } from "./InfiniteFooter";

describe("InfiniteFooter", () => {
  it("renders nothing when there is no further page", () => {
    const { container } = render(
      <InfiniteFooter hasMore={false} isLoadingMore={false} onLoadMore={vi.fn()} />,
    );
    expect(container).toBeEmptyDOMElement();
  });

  it("renders a Load more button and invokes onLoadMore on click", () => {
    const onLoadMore = vi.fn();
    render(<InfiniteFooter hasMore isLoadingMore={false} onLoadMore={onLoadMore} />);
    const btn = screen.getByRole("button", { name: /load more/i });
    fireEvent.click(btn);
    expect(onLoadMore).toHaveBeenCalledTimes(1);
  });

  it("disables the button and shows progress while loading", () => {
    render(<InfiniteFooter hasMore isLoadingMore onLoadMore={vi.fn()} />);
    const btn = screen.getByRole("button", { name: /loading more/i });
    expect(btn).toBeDisabled();
  });
});
