import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { AdminCollectionsPage } from "./AdminCollectionsPage";

vi.mock("@/api/admin/hooks", () => ({
  useInfiniteAdminCollections: vi.fn(),
}));

import { useInfiniteAdminCollections } from "@/api/admin/hooks";
const mockUseAdminCollections = vi.mocked(useInfiniteAdminCollections);

function wrapper({ children }: { children: React.ReactNode }) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
}

function makeCollection(overrides: Record<string, unknown> = {}) {
  return {
    id: "col-agent-001",
    owner_id: "apikey:ingest",
    owner_email: "ingest@apikey.local",
    name: "Warehouse Onboarding Notes",
    description: "",
    config: {},
    sections: [],
    asset_tags: [],
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-06-01T00:00:00Z",
    ...overrides,
  };
}

function mockList(collections: Record<string, unknown>[], total = collections.length) {
  mockUseAdminCollections.mockReturnValue({
    data: { data: collections, total, limit: 50, offset: 0, share_summaries: {} },
    isLoading: false,
    hasNextPage: false,
    isFetchingNextPage: false,
    fetchNextPage: vi.fn(),
  } as unknown as ReturnType<typeof useInfiniteAdminCollections>);
}

describe("AdminCollectionsPage (#1292)", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("lists a collection owned by a principal the reader is not", () => {
    mockList([makeCollection()]);
    render(<AdminCollectionsPage onNavigate={vi.fn()} />, { wrapper });

    expect(screen.getByText("Warehouse Onboarding Notes")).toBeInTheDocument();
    // The owner is the whole point of the admin list: it is the only place the
    // identity that owns an orphaned collection is visible.
    expect(screen.getByText("ingest@apikey.local")).toBeInTheDocument();
  });

  it("opens the admin viewer on row click", () => {
    const onNavigate = vi.fn();
    mockList([makeCollection()]);
    render(<AdminCollectionsPage onNavigate={onNavigate} />, { wrapper });

    fireEvent.click(screen.getByText("Warehouse Onboarding Notes"));
    expect(onNavigate).toHaveBeenCalledWith("/admin/collections/col-agent-001");
  });

  it("moves between the Assets and Collections faces of the admin section", () => {
    const onNavigate = vi.fn();
    mockList([makeCollection()]);
    render(<AdminCollectionsPage onNavigate={onNavigate} />, { wrapper });

    fireEvent.mouseDown(screen.getByRole("tab", { name: "Assets" }));
    expect(onNavigate).toHaveBeenCalledWith("/admin/assets");
  });

  it("says so when no collection matches", () => {
    mockList([]);
    render(<AdminCollectionsPage onNavigate={vi.fn()} />, { wrapper });

    expect(screen.getByText("No collections found")).toBeInTheDocument();
  });

  it("searches by what the reader typed once typing settles", async () => {
    mockList([makeCollection()]);
    render(<AdminCollectionsPage onNavigate={vi.fn()} />, { wrapper });

    fireEvent.change(screen.getByPlaceholderText(/search by name/i), {
      target: { value: "ingest" },
    });

    // The term reaches the query only after the debounce, so the list is not
    // refetched on every keystroke.
    expect(mockUseAdminCollections).not.toHaveBeenCalledWith({ search: "ingest" });
    await waitFor(() =>
      expect(mockUseAdminCollections).toHaveBeenCalledWith({ search: "ingest" }),
    );
  });
});
