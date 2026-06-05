import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MyAssetsPage } from "./MyAssetsPage";

// Mock the useAssets hook
vi.mock("@/api/portal/hooks", () => ({
  useAssets: vi.fn(),
}));

import { useAssets } from "@/api/portal/hooks";
const mockUseAssets = vi.mocked(useAssets);

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

  it("searching does not crash when an asset has no description", () => {
    // Regression: the API serializes description with `omitempty`, so an
    // asset with no description arrives as undefined. The client-side search
    // filter called `description.toLowerCase()` unguarded and crashed the
    // page the moment the user typed anything.
    mockUseAssets.mockReturnValue({
      data: {
        // The search term below must NOT match the name, so the filter
        // falls through to the (undefined) description term that crashed.
        data: [makeAsset({ name: "Annual Summary", description: undefined })],
        total: 1,
        limit: 50,
        offset: 0,
        share_summaries: {},
      },
      isLoading: false,
    } as unknown as ReturnType<typeof useAssets>);

    render(<MyAssetsPage onNavigate={vi.fn()} />, { wrapper });
    expect(screen.getByText("Annual Summary")).toBeInTheDocument();

    // Typing a query that matches neither the name nor the absent description
    // forces evaluation of the description branch of the filter.
    expect(() =>
      fireEvent.change(screen.getByPlaceholderText("Search assets..."), {
        target: { value: "revenue" },
      }),
    ).not.toThrow();

    // The descriptionless, non-matching asset is filtered out without crashing.
    expect(screen.queryByText("Annual Summary")).not.toBeInTheDocument();
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
