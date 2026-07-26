import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { KnowledgePageList } from "./KnowledgePageList";

// The list's data hooks are stubbed so the empty state renders without a live
// query; MIN_SEARCH_LEN is re-exported unchanged because the component reads it
// to decide whether it is browsing or searching.
vi.mock("@/api/portal/hooks", () => ({
  useInfiniteKnowledgePages: vi.fn(() => ({
    data: { data: [], total: 0, limit: 100, offset: 0 },
    isLoading: false,
    isError: false,
    hasNextPage: false,
    isFetchingNextPage: false,
    fetchNextPage: vi.fn(),
  })),
  useSearchKnowledgePages: vi.fn(() => ({ data: undefined, isLoading: false, isError: false })),
  useThreadCounts: vi.fn(() => ({ data: {} })),
  MIN_SEARCH_LEN: 3,
}));

describe("KnowledgePageList empty state", () => {
  // #1015: a knowledgebase with nothing in it is where someone decides whether
  // their reference file becomes a page. It should not.
  it("points files meant to be used verbatim at resources", () => {
    render(<KnowledgePageList canEdit onOpen={vi.fn()} onCreate={vi.fn()} />);

    expect(screen.getByText(/No knowledge pages yet\./)).toBeInTheDocument();
    expect(
      screen.getByText(/A file you wrote and want\s+used as-is belongs in Resources\./),
    ).toBeInTheDocument();
  });

  // The guidance is not gated on edit rights: a reader who cannot create a page
  // is exactly the person who needs to be told where their file goes instead.
  it("shows the cross-reference to a reader who cannot create pages", () => {
    render(<KnowledgePageList canEdit={false} onOpen={vi.fn()} onCreate={vi.fn()} />);

    expect(screen.getByText(/A file you wrote and want\s+used as-is belongs in Resources\./)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Create the first page" })).not.toBeInTheDocument();
  });
});
