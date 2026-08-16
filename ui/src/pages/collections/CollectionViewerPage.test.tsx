import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { CollectionViewerPage } from "./CollectionViewerPage";
import { CollectionEditorPage } from "./CollectionEditorPage";

// The viewer's action row is driven entirely by the authority the server
// resolved, so the collection hook is the only thing these tests vary.
vi.mock("@/api/portal/hooks", () => ({
  useCollection: vi.fn(),
  useDeleteCollection: vi.fn(() => ({ mutateAsync: vi.fn(), isPending: false })),
  useUpdateCollection: vi.fn(() => ({ mutateAsync: vi.fn(), isPending: false })),
  useUpdateCollectionSections: vi.fn(() => ({ mutateAsync: vi.fn(), isPending: false })),
  useUpdateCollectionConfig: vi.fn(() => ({ mutate: vi.fn(), mutateAsync: vi.fn(), isPending: false })),
  useAssets: vi.fn(() => ({ data: { data: [] } })),
}));

vi.mock("@/components/CollectionThumbnailQueue", () => ({
  CollectionThumbnailGenerator: () => null,
}));

vi.mock("@/components/knowledge/KnowledgeBacklinks", () => ({
  KnowledgeBacklinks: () => null,
}));

vi.mock("@/components/ShareDialog", () => ({
  ShareDialog: () => null,
}));

vi.mock("@/components/feedback/FeedbackButton", () => ({
  FeedbackButton: ({ canModerate }: { canModerate?: boolean }) => (
    <button type="button">{canModerate ? "Feedback (moderator)" : "Feedback"}</button>
  ),
}));

import { useCollection } from "@/api/portal/hooks";
const mockUseCollection = vi.mocked(useCollection);

function wrapper({ children }: { children: React.ReactNode }) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
}

function collectionWith(authority: {
  is_owner: boolean;
  can_edit: boolean;
  can_manage: boolean;
  share_permission?: string;
}) {
  return {
    id: "c1",
    owner_id: "u-owner",
    owner_email: "owner@example.com",
    name: "Quarterly Reviews",
    description: "",
    thumbnail_s3_key: "",
    sections: [],
    config: {},
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-06-01T00:00:00Z",
    ...authority,
  };
}

function renderViewer() {
  return render(
    <CollectionViewerPage collectionId="c1" onNavigate={vi.fn()} onBack={vi.fn()} />,
    { wrapper },
  );
}

describe("CollectionViewerPage: what an Editor share may do (#1294)", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("gives the owner every action", () => {
    mockUseCollection.mockReturnValue({
      data: collectionWith({ is_owner: true, can_edit: true, can_manage: true }),
      isLoading: false,
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
    } as any);
    renderViewer();

    expect(screen.getByRole("button", { name: /edit/i })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /share/i })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /delete/i })).toBeInTheDocument();
    expect(screen.queryByText(/^Shared/)).not.toBeInTheDocument();
  });

  it("gives an Editor the edit control but not share or delete", () => {
    mockUseCollection.mockReturnValue({
      data: collectionWith({
        is_owner: false,
        can_edit: true,
        can_manage: false,
        share_permission: "editor",
      }),
      isLoading: false,
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
    } as any);
    renderViewer();

    expect(screen.getByRole("button", { name: /edit/i })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /share/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /delete/i })).not.toBeInTheDocument();
    expect(screen.getByText("Shared (Editor)")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /moderator/i })).toBeInTheDocument();
  });

  it("gives a Viewer no write action at all", () => {
    mockUseCollection.mockReturnValue({
      data: collectionWith({
        is_owner: false,
        can_edit: false,
        can_manage: false,
        share_permission: "viewer",
      }),
      isLoading: false,
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
    } as any);
    renderViewer();

    expect(screen.queryByRole("button", { name: /edit/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /share/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /delete/i })).not.toBeInTheDocument();
    expect(screen.getByText("Shared (Viewer)")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /moderator/i })).not.toBeInTheDocument();
  });
});

describe("CollectionEditorPage: authority the editor page reads", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  function renderEditor() {
    return render(
      <CollectionEditorPage collectionId="c1" onBack={vi.fn()} onNavigate={vi.fn()} />,
      { wrapper },
    );
  }

  it("hides Delete from an Editor who may not manage the collection", () => {
    mockUseCollection.mockReturnValue({
      data: collectionWith({ is_owner: false, can_edit: true, can_manage: false }),
      isLoading: false,
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
    } as any);
    renderEditor();

    expect(screen.getByRole("button", { name: /save/i })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /delete/i })).not.toBeInTheDocument();
  });

  it("refuses a Viewer the form rather than letting Save fail", () => {
    mockUseCollection.mockReturnValue({
      data: collectionWith({ is_owner: false, can_edit: false, can_manage: false }),
      isLoading: false,
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
    } as any);
    renderEditor();

    expect(screen.getByText(/view access to this collection/i)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /save/i })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: /back to collection/i })).toBeInTheDocument();
  });
});
