import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
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
