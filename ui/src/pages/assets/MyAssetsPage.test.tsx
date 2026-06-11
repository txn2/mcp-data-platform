import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent, act } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MyAssetsPage } from "./MyAssetsPage";

// Mock the asset hooks. useSearchAssets is called unconditionally by the page
// (its result is only read while a search is active); stub it with a safe idle
// default so these share-icon tests render without a live query.
vi.mock("@/api/portal/hooks", () => ({
  useAssets: vi.fn(),
  useSearchAssets: vi.fn(() => ({ data: undefined, isLoading: false })),
  useThreadCounts: vi.fn(() => ({ data: {} })),
}));

import { useAssets, useSearchAssets } from "@/api/portal/hooks";
const mockUseAssets = vi.mocked(useAssets);
const mockUseSearchAssets = vi.mocked(useSearchAssets);

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
    } as unknown as ReturnType<typeof useAssets>);

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
    } as unknown as ReturnType<typeof useAssets>);

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
    } as unknown as ReturnType<typeof useAssets>);

    render(<MyAssetsPage onNavigate={vi.fn()} />, { wrapper });

    expect(screen.queryByTitle("Shared with users")).not.toBeInTheDocument();
    expect(screen.getByTitle("Has public link")).toBeInTheDocument();
  });

  it("renders ranked search results without crashing on a missing description", () => {
    // Search is server-side (useSearchAssets). The API serializes description
    // with `omitempty`, so a ranked result can arrive with description
    // undefined; rendering it must not crash. Typing debounces 300ms before the
    // ranked results replace the browse list.
    mockUseAssets.mockReturnValue({
      data: { data: [makeAsset({ name: "Annual Summary" })], total: 1, limit: 50, offset: 0, share_summaries: {} },
      isLoading: false,
    } as unknown as ReturnType<typeof useAssets>);
    mockUseSearchAssets.mockReturnValue({
      data: {
        data: [{ asset: makeAsset({ id: "r1", name: "Revenue Report", description: undefined }), score: 0.9 }],
      },
      isLoading: false,
    } as unknown as ReturnType<typeof useSearchAssets>);

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
    } as unknown as ReturnType<typeof useAssets>);

    render(<MyAssetsPage onNavigate={vi.fn()} />, { wrapper });

    expect(screen.queryByTitle("Shared with users")).not.toBeInTheDocument();
    expect(screen.queryByTitle("Has public link")).not.toBeInTheDocument();

    // Title should still render correctly
    expect(
      screen.getByText("A very long asset name that should be truncated before it reaches the icons"),
    ).toBeInTheDocument();
  });
});
