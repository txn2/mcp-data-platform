import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { CollectionsPage } from "./CollectionsPage";

// Mock the collection hooks. The page calls the search and facet hooks
// unconditionally, so they get idle stubs; the browse hook is the one these
// tests observe.
vi.mock("@/api/portal/hooks", () => ({
  useInfiniteCollections: vi.fn(),
  useInfiniteSearchCollections: vi.fn(() => ({ data: undefined, isLoading: false })),
  useCollectionFacets: vi.fn(() => ({ data: { authors: [{ value: "bob@example.com", count: 2 }] } })),
  useThreadCounts: vi.fn(() => ({ data: {} })),
  useCreateCollection: vi.fn(() => ({ mutateAsync: vi.fn(), isPending: false })),
}));


import { useInfiniteCollections } from "@/api/portal/hooks";
const mockUseCollections = vi.mocked(useInfiniteCollections);

function wrapper({ children }: { children: React.ReactNode }) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
}

function makeCollection(overrides: Record<string, unknown> = {}) {
  return {
    id: "c1",
    owner_id: "u1",
    owner_email: "test@example.com",
    name: "Quarterly Reviews",
    description: "",
    thumbnail_s3_key: "",
    asset_tags: [],
    created_at: "2025-01-01T00:00:00Z",
    updated_at: "2025-06-01T00:00:00Z",
    ...overrides,
  };
}

// chooseOption drives the Radix listbox: jsdom has no PointerEvent, so the
// trigger's pointerdown handler never fires and it is opened from the keyboard.
function chooseOption(name: string, option: string): void {
  fireEvent.keyDown(screen.getByRole("combobox", { name }), { key: "Enter" });
  fireEvent.click(screen.getByRole("option", { name: option }));
}

function lastRequestedParams(): Record<string, string> | undefined {
  const calls = mockUseCollections.mock.calls;
  return calls[calls.length - 1]?.[0] as Record<string, string> | undefined;
}

describe("CollectionsPage: ordering (#1295)", () => {
  beforeEach(() => {
    mockUseCollections.mockReturnValue({
      data: { data: [makeCollection()], total: 1, limit: 50, offset: 0, share_summaries: {} },
      isLoading: false,
    } as unknown as ReturnType<typeof useInfiniteCollections>);
  });

  it("opens on most recently updated, like the Assets tab beside it", () => {
    render(<CollectionsPage onNavigate={vi.fn()} />, { wrapper });

    expect(lastRequestedParams()).toMatchObject({ sort: "updated_at", dir: "desc" });
  });

  it("re-asks the server when the column changes", () => {
    render(<CollectionsPage onNavigate={vi.fn()} />, { wrapper });

    chooseOption("Sort by", "Name");

    expect(lastRequestedParams()).toMatchObject({ sort: "name", dir: "asc" });
  });

  it("offers no size ordering, because a collection has no size", () => {
    render(<CollectionsPage onNavigate={vi.fn()} />, { wrapper });

    fireEvent.keyDown(screen.getByRole("combobox", { name: "Sort by" }), { key: "Enter" });

    expect(screen.queryByRole("option", { name: "Size" })).not.toBeInTheDocument();
  });

  it("shows the timestamp the list is ordered by", () => {
    render(<CollectionsPage onNavigate={vi.fn()} />, { wrapper });
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
});

describe("CollectionsPage: scope and authors (#2500)", () => {
  beforeEach(() => {
    mockUseCollections.mockReturnValue({
      data: {
        data: [makeCollection({ id: "c2", name: "Bob's board", owner_id: "u-bob", owner_email: "bob@example.com" })],
        total: 1,
        limit: 50,
        offset: 0,
        permissions: { c2: "editor" },
      },
      isLoading: false,
    } as unknown as ReturnType<typeof useInfiniteCollections>);
  });

  it("asks the server for the scope and the authors chosen, and names who shared a row", () => {
    render(<CollectionsPage onNavigate={vi.fn()} />, { wrapper });
    const last = () => mockUseCollections.mock.calls[mockUseCollections.mock.calls.length - 1]?.[0];

    expect(last()).toMatchObject({ scope: "all" });
    expect(screen.getByText(/Shared by bob@example.com/)).toBeInTheDocument();

    fireEvent.keyDown(screen.getByRole("button", { name: "Filter by author" }), { key: "Enter" });
    fireEvent.click(screen.getByRole("menuitemcheckbox", { name: /bob@example.com/ }));
    expect(last()).toMatchObject({ owners: ["bob@example.com"] });
  });
});
