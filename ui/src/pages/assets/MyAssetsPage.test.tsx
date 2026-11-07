import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, act } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MyAssetsPage } from "./MyAssetsPage";

// Mock the asset hooks. The search and facet hooks are called unconditionally
// by the page; stub them with safe idle defaults so these tests render without
// a live query. The infinite hooks expose the same accumulated `data` page
// shape as the underlying paginated response.
vi.mock("@/api/portal/hooks", () => ({
  useInfiniteAssets: vi.fn(),
  useInfiniteSearchAssets: vi.fn(() => ({ data: undefined, isLoading: false })),
  useAssetFacets: vi.fn(() => ({ data: undefined })),
  useThreadCounts: vi.fn(() => ({ data: {} })),
}));

import { useAssetFacets, useInfiniteAssets, useInfiniteSearchAssets } from "@/api/portal/hooks";
const mockUseAssets = vi.mocked(useInfiniteAssets);
const mockUseSearchAssets = vi.mocked(useInfiniteSearchAssets);
const mockUseFacets = vi.mocked(useAssetFacets);

// The Mine/Shared/All scope persists to localStorage; reset it so each test
// starts on the default "all" scope.
beforeEach(() => {
  globalThis.localStorage?.clear?.();
  mockUseSearchAssets.mockReturnValue({ data: undefined, isLoading: false } as unknown as ReturnType<typeof useInfiniteSearchAssets>);
  mockUseFacets.mockReturnValue({ data: undefined } as unknown as ReturnType<typeof useAssetFacets>);
});

function wrapper({ children }: { children: React.ReactNode }) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
}

function makeAsset(overrides: Record<string, unknown> = {}) {
  return {
    id: "a1",
    owner_id: "u1",
    owner_email: "test@example.com",
    name: "A very long asset name that should be truncated before it reaches the icons",
    description: "desc",
    content_type: "text/html",
    s3_bucket: "b",
    s3_key: "k",
    size_bytes: 1024,
    tags: [],
    provenance: {},
    session_id: "s1",
    created_at: "2025-01-01T00:00:00Z",
    updated_at: "2025-01-01T00:00:00Z",
    ...overrides,
  };
}

describe("MyAssetsPage: share icons overlay on card thumbnail", () => {
  it("both share icons appear when asset has user share and public link", () => {
    mockUseAssets.mockReturnValue({
      data: {
        data: [makeAsset()],
        total: 1,
        limit: 50,
        offset: 0,
        share_summaries: {
          a1: { has_user_share: true, has_public_link: true },
        },
      },
      isLoading: false,
    } as unknown as ReturnType<typeof useInfiniteAssets>);

    render(<MyAssetsPage onNavigate={vi.fn()} />, { wrapper });

    expect(screen.getByTitle("Shared with users")).toBeInTheDocument();
    expect(screen.getByTitle("Has public link")).toBeInTheDocument();

    // Share icons are in an overlay container positioned on the card (top-2 right-2)
    const shareIcon = screen.getByTitle("Shared with users");
    const iconContainer = shareIcon.parentElement!;
    expect(iconContainer.className).toContain("absolute");
    expect(iconContainer.className).toContain("top-2");
    expect(iconContainer.className).toContain("right-2");
  });

  it("only user share icon when has_public_link is false", () => {
    mockUseAssets.mockReturnValue({
      data: {
        data: [makeAsset()],
        total: 1,
        limit: 50,
        offset: 0,
        share_summaries: {
          a1: { has_user_share: true, has_public_link: false },
        },
      },
      isLoading: false,
    } as unknown as ReturnType<typeof useInfiniteAssets>);

    render(<MyAssetsPage onNavigate={vi.fn()} />, { wrapper });

    expect(screen.getByTitle("Shared with users")).toBeInTheDocument();
    expect(screen.queryByTitle("Has public link")).not.toBeInTheDocument();
  });

  it("only public link icon when has_user_share is false", () => {
    mockUseAssets.mockReturnValue({
      data: {
        data: [makeAsset()],
        total: 1,
        limit: 50,
        offset: 0,
        share_summaries: {
          a1: { has_user_share: false, has_public_link: true },
        },
      },
      isLoading: false,
    } as unknown as ReturnType<typeof useInfiniteAssets>);

    render(<MyAssetsPage onNavigate={vi.fn()} />, { wrapper });

    expect(screen.queryByTitle("Shared with users")).not.toBeInTheDocument();
    expect(screen.getByTitle("Has public link")).toBeInTheDocument();
  });

  it("shows a markdown description as readable text on the card (#1101)", () => {
    // Asset descriptions are authored as markdown. A card has two clamped
    // lines, so the source is stripped to prose rather than rendered or shown
    // with its own asterisks and backticks.
    mockUseAssets.mockReturnValue({
      data: {
        data: [makeAsset({ description: "**Revenue** rollup from `orders`" })],
        total: 1,
        limit: 50,
        offset: 0,
        share_summaries: {},
      },
      isLoading: false,
    } as unknown as ReturnType<typeof useInfiniteAssets>);

    render(<MyAssetsPage onNavigate={vi.fn()} />, { wrapper });

    expect(screen.getByText("Revenue rollup from orders")).toBeInTheDocument();
    expect(document.body.textContent).not.toContain("**Revenue**");
  });

  it("renders ranked search results without crashing on a missing description", () => {
    // Search is server-side (useInfiniteSearchAssets). The API serializes description
    // with `omitempty`, so a ranked result can arrive with description
    // undefined; rendering it must not crash. Typing debounces 300ms before the
    // ranked results replace the browse list.
    mockUseAssets.mockReturnValue({
      data: { data: [makeAsset({ name: "Annual Summary" })], total: 1, limit: 50, offset: 0, share_summaries: {} },
      isLoading: false,
    } as unknown as ReturnType<typeof useInfiniteAssets>);
    mockUseSearchAssets.mockReturnValue({
      data: {
        data: [{ asset: makeAsset({ id: "r1", name: "Revenue Report", description: undefined }), score: 0.9 }],
        total: 1,
      },
      isLoading: false,
    } as unknown as ReturnType<typeof useInfiniteSearchAssets>);

    vi.useFakeTimers();
    try {
      render(<MyAssetsPage onNavigate={vi.fn()} />, { wrapper });
      expect(screen.getByText("Annual Summary")).toBeInTheDocument();

      expect(() =>
        fireEvent.change(screen.getByPlaceholderText("Search assets by meaning..."), {
          target: { value: "revenue" },
        }),
      ).not.toThrow();

      // Advance past the 300ms debounce so the ranked results take over.
      act(() => {
        vi.advanceTimersByTime(300);
      });

      // The descriptionless ranked result renders; the browse list is replaced.
      expect(screen.getByText("Revenue Report")).toBeInTheDocument();
      expect(screen.queryByText("Annual Summary")).not.toBeInTheDocument();
      // Every scope is ranked by the server, in the scope on screen (#2500).
      const calls = mockUseSearchAssets.mock.calls;
      expect(calls[calls.length - 1]).toEqual(["revenue", expect.objectContaining({ scope: "all" })]);
      expect(screen.getByText(/1 match for/)).toBeInTheDocument();
    } finally {
      vi.useRealTimers();
    }
  });

  it("no share icons when share_summaries is empty", () => {
    mockUseAssets.mockReturnValue({
      data: {
        data: [makeAsset()],
        total: 1,
        limit: 50,
        offset: 0,
        share_summaries: {},
      },
      isLoading: false,
    } as unknown as ReturnType<typeof useInfiniteAssets>);

    render(<MyAssetsPage onNavigate={vi.fn()} />, { wrapper });

    expect(screen.queryByTitle("Shared with users")).not.toBeInTheDocument();
    expect(screen.queryByTitle("Has public link")).not.toBeInTheDocument();

    // Title should still render correctly
    expect(
      screen.getByText("A very long asset name that should be truncated before it reaches the icons"),
    ).toBeInTheDocument();
  });
});

describe("MyAssetsPage: scope filter and tabs", () => {
  it("asks the server for the scope on screen and names who shared each row (#2500)", () => {
    mockUseAssets.mockReturnValue({
      data: {
        data: [
          makeAsset({ id: "mine1", name: "My Own Asset" }),
          makeAsset({ id: "shared1", name: "Shared Chart", owner_id: "u-carol", owner_email: "carol@example.com" }),
        ],
        total: 2,
        limit: 50,
        offset: 0,
        share_summaries: {},
        permissions: { shared1: "viewer" },
      },
      isLoading: false,
    } as unknown as ReturnType<typeof useInfiniteAssets>);

    render(<MyAssetsPage onNavigate={vi.fn()} />, { wrapper });

    // Default scope is "all": the server returns both, and the row the caller
    // does not own names its owner as the one who shared it.
    expect(lastRequestedParams()).toMatchObject({ scope: "all" });
    expect(screen.getByText("My Own Asset")).toBeInTheDocument();
    expect(screen.getByText(/Shared by carol@example.com/)).toBeInTheDocument();

    fireEvent.mouseDown(screen.getByRole("tab", { name: "Shared" }));
    expect(lastRequestedParams()).toMatchObject({ scope: "shared" });
    fireEvent.mouseDown(screen.getByRole("tab", { name: "Mine" }));
    expect(lastRequestedParams()).toMatchObject({ scope: "mine" });
  });

  it("offers the tags and authors the server counted, and sends the choice to it", () => {
    mockUseAssets.mockReturnValue({
      data: { data: [makeAsset()], total: 1, limit: 50, offset: 0, share_summaries: {} },
      isLoading: false,
    } as unknown as ReturnType<typeof useInfiniteAssets>);
    mockUseFacets.mockReturnValue({
      data: {
        tags: [{ value: "finance", count: 42 }],
        authors: [{ value: "bob@example.com", count: 7 }],
      },
    } as unknown as ReturnType<typeof useAssetFacets>);

    render(<MyAssetsPage onNavigate={vi.fn()} />, { wrapper });

    chooseOption("Filter by tag", "finance (42)");
    expect(lastRequestedParams()).toMatchObject({ tag: "finance" });
    expect(mockUseFacets).toHaveBeenLastCalledWith(expect.objectContaining({ tag: "finance", scope: "all" }));

    fireEvent.keyDown(screen.getByRole("button", { name: "Filter by author" }), { key: "Enter" });
    fireEvent.click(screen.getByRole("menuitemcheckbox", { name: /bob@example.com/ }));
    expect(lastRequestedParams()).toMatchObject({ owners: ["bob@example.com"] });
  });

  it("the Collections tab navigates to /collections", () => {
    mockUseAssets.mockReturnValue({
      data: { data: [], total: 0, limit: 50, offset: 0, share_summaries: {} },
      isLoading: false,
    } as unknown as ReturnType<typeof useInfiniteAssets>);

    const onNavigate = vi.fn();
    render(<MyAssetsPage onNavigate={onNavigate} />, { wrapper });

    // AssetsTabs rides ui/tabs, so its faces are tabs rather than buttons.
    fireEvent.mouseDown(screen.getByRole("tab", { name: "Collections" }));
    expect(onNavigate).toHaveBeenCalledWith("/collections");
  });
});

describe("MyAssetsPage: load more pagination", () => {
  it("shows Load more when more pages exist and fetches the next page on click", () => {
    const fetchNextPage = vi.fn();
    mockUseAssets.mockReturnValue({
      // First page of a larger set: 1 of 3 loaded, another page available.
      data: { data: [makeAsset({ id: "p1", name: "Page One Asset" })], total: 3, limit: 50, offset: 0, share_summaries: {} },
      isLoading: false,
      hasNextPage: true,
      isFetchingNextPage: false,
      fetchNextPage,
    } as unknown as ReturnType<typeof useInfiniteAssets>);

    render(<MyAssetsPage onNavigate={vi.fn()} />, { wrapper });
    // Mine scope draws only from the browse (useInfiniteAssets) list.
    fireEvent.mouseDown(screen.getByRole("tab", { name: "Mine" }));

    const button = screen.getByRole("button", { name: "Load more" });
    fireEvent.click(button);
    expect(fetchNextPage).toHaveBeenCalledTimes(1);
  });

  it("hides Load more on the final page", () => {
    mockUseAssets.mockReturnValue({
      data: { data: [makeAsset()], total: 1, limit: 50, offset: 0, share_summaries: {} },
      isLoading: false,
      hasNextPage: false,
      isFetchingNextPage: false,
      fetchNextPage: vi.fn(),
    } as unknown as ReturnType<typeof useInfiniteAssets>);

    render(<MyAssetsPage onNavigate={vi.fn()} />, { wrapper });
    fireEvent.mouseDown(screen.getByRole("tab", { name: "Mine" }));

    expect(screen.queryByRole("button", { name: "Load more" })).not.toBeInTheDocument();
  });

  // #1015: the moment someone has nowhere to put a file they wrote themselves
  // is the moment to say assets are not that place.
  it("points human-authored files at resources when there are no assets yet", () => {
    mockUseAssets.mockReturnValue({
      data: { data: [], total: 0, limit: 50, offset: 0, share_summaries: {} },
      isLoading: false,
      hasNextPage: false,
      isFetchingNextPage: false,
      fetchNextPage: vi.fn(),
    } as unknown as ReturnType<typeof useInfiniteAssets>);

    render(<MyAssetsPage onNavigate={vi.fn()} />, { wrapper });
    fireEvent.mouseDown(screen.getByRole("tab", { name: "Mine" }));

    expect(screen.getByText("No assets yet")).toBeInTheDocument();
    expect(
      screen.getByText("A file you wrote yourself and want used as-is belongs in Resources, not here."),
    ).toBeInTheDocument();
  });
});

// chooseOption drives one of the Radix listbox facets: open the trigger by its
// accessible name, then click the named option. jsdom has no PointerEvent, so
// the trigger's pointerdown handler never fires and the listbox is opened with
// the keyboard instead (see ui/README.md).
function chooseOption(name: string, option: string): void {
  fireEvent.keyDown(screen.getByRole("combobox", { name }), { key: "Enter" });
  fireEvent.click(screen.getByRole("option", { name: option }));
}

/** The `params` the page last asked the browse query for. */
function lastRequestedParams(): Record<string, unknown> | undefined {
  const calls = mockUseAssets.mock.calls;
  return calls[calls.length - 1]?.[0] as Record<string, unknown> | undefined;
}

describe("MyAssetsPage: ordering (#1295)", () => {
  beforeEach(() => {
    mockUseAssets.mockReturnValue({
      data: {
        data: [
          makeAsset({ created_at: "2025-01-01T00:00:00Z", updated_at: "2025-06-01T00:00:00Z" }),
        ],
        total: 1,
        limit: 50,
        offset: 0,
        share_summaries: {},
      },
      isLoading: false,
    } as unknown as ReturnType<typeof useInfiniteAssets>);
  });

  it("opens on most recently updated, and says so to the server", () => {
    render(<MyAssetsPage onNavigate={vi.fn()} />, { wrapper });

    expect(lastRequestedParams()).toMatchObject({ sort: "updated_at", dir: "desc" });
  });

  it("re-asks the server when the column changes, at that column's direction", () => {
    render(<MyAssetsPage onNavigate={vi.fn()} />, { wrapper });

    chooseOption("Sort by", "Name");

    // Ordering is server-side: the library is paginated, so re-sorting the
    // page already loaded would only reorder the first fifty rows.
    expect(lastRequestedParams()).toMatchObject({ sort: "name", dir: "asc" });
  });

  it("reverses without changing the column", () => {
    render(<MyAssetsPage onNavigate={vi.fn()} />, { wrapper });

    fireEvent.click(screen.getByRole("button", { name: /sort ascending/i }));

    expect(lastRequestedParams()).toMatchObject({ sort: "updated_at", dir: "asc" });
  });

  it("shows the timestamp the list is ordered by, not a different one", () => {
    // The complaint that opened #1295: a list ordered by one date while
    // displaying another reads as broken, because the visible dates are not in
    // the order the rows are.
    render(<MyAssetsPage onNavigate={vi.fn()} />, { wrapper });
    fireEvent.click(screen.getByRole("button", { name: "Table view" }));

    expect(screen.getByRole("columnheader", { name: /Updated/ })).toBeInTheDocument();
    expect(
      screen.getByText(new Date("2025-06-01T00:00:00Z").toLocaleDateString()),
    ).toBeInTheDocument();

    chooseOption("Sort by", "Created");

    expect(screen.getByRole("columnheader", { name: /Created/ })).toBeInTheDocument();
    expect(
      screen.getByText(new Date("2025-01-01T00:00:00Z").toLocaleDateString()),
    ).toBeInTheDocument();
  });

  it("sorts from a table header, and reverses on a second click", () => {
    render(<MyAssetsPage onNavigate={vi.fn()} />, { wrapper });
    fireEvent.click(screen.getByRole("button", { name: "Table view" }));

    fireEvent.click(screen.getByRole("columnheader", { name: /Name/ }));
    expect(lastRequestedParams()).toMatchObject({ sort: "name", dir: "asc" });

    fireEvent.click(screen.getByRole("columnheader", { name: /Name/ }));
    expect(lastRequestedParams()).toMatchObject({ sort: "name", dir: "desc" });
  });
});

describe("MyAssetsPage: an incomplete export (#2057)", () => {
  function listOne(tags: string[]) {
    mockUseAssets.mockReturnValue({
      data: { data: [makeAsset({ tags })], total: 1, limit: 50, offset: 0 },
      isLoading: false,
    } as unknown as ReturnType<typeof useInfiniteAssets>);
  }

  it("marks an asset carrying the truncated tag on the card and in the table", () => {
    listOne(["contacts", "_sys-truncated"]);
    render(<MyAssetsPage onNavigate={vi.fn()} />, { wrapper });
    expect(screen.getByText("Incomplete")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Table view" }));
    expect(screen.getByText("Incomplete")).toBeInTheDocument();
  });

  it("does not mark a complete asset", () => {
    listOne(["contacts"]);
    render(<MyAssetsPage onNavigate={vi.fn()} />, { wrapper });
    expect(screen.queryByText("Incomplete")).not.toBeInTheDocument();
  });
});
