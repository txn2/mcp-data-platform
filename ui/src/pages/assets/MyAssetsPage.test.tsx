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

describe("MyAssetsPage: title does not overlap share icons", () => {
  it("title row has pr-12 padding to clear absolutely-positioned share icons", () => {
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

    // The share icons should be present
    expect(screen.getByTitle("Shared with users")).toBeInTheDocument();
    expect(screen.getByTitle("Has public link")).toBeInTheDocument();

    // The title text element
    const titleSpan = screen.getByText(
      "A very long asset name that should be truncated before it reaches the icons",
    );
    // The title row is the parent div containing the icon + title span
    const titleRow = titleSpan.closest("div");
    expect(titleRow).not.toBeNull();
    expect(titleRow!.className).toContain("pr-12");

    // The share icon container should be absolutely positioned.
    // Structure: <button> > <div class="absolute ..."> > <span title="..."> > <svg>
    // So shareIcon's parentElement is the <span>, and its parentElement is the <div>.
    const shareIcon = screen.getByTitle("Shared with users");
    // shareIcon is the <span>, its parent is the absolute div
    const iconContainer = shareIcon.parentElement!;
    expect(iconContainer.className).toContain("absolute");
    expect(iconContainer.className).toContain("top-4");
    expect(iconContainer.className).toContain("right-4");
  });

  it("title row has pr-12 with only one share icon (user share)", () => {
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

    const titleSpan = screen.getByText(
      "A very long asset name that should be truncated before it reaches the icons",
    );
    const titleRow = titleSpan.closest("div");
    expect(titleRow!.className).toContain("pr-12");
  });

  it("title row has pr-12 with only one share icon (public link)", () => {
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

    const titleSpan = screen.getByText(
      "A very long asset name that should be truncated before it reaches the icons",
    );
    const titleRow = titleSpan.closest("div");
    expect(titleRow!.className).toContain("pr-12");
  });

  it("title row has pr-12 even when no share icons are present", () => {
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

    const titleSpan = screen.getByText(
      "A very long asset name that should be truncated before it reaches the icons",
    );
    const titleRow = titleSpan.closest("div");
    expect(titleRow).not.toBeNull();
    // pr-12 is always applied regardless of icon presence (avoids layout shift)
    expect(titleRow!.className).toContain("pr-12");
  });
});
