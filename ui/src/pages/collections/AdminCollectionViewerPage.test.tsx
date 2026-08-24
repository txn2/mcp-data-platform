import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { AdminCollectionViewerPage } from "./AdminCollectionViewerPage";

const updateMutate = vi.fn();
const deleteMutate = vi.fn();
// Read on every render, so a test can put the update mutation into its error
// state without re-mocking the module.
const updateState = { isPending: false, isError: false };

vi.mock("@/api/admin/hooks", () => ({
  useAdminCollection: vi.fn(),
  useAdminUpdateCollection: vi.fn(() => ({ mutateAsync: updateMutate, ...updateState })),
  useAdminDeleteCollection: vi.fn(() => ({ mutateAsync: deleteMutate, isPending: false })),
}));

vi.mock("@/components/ShareDialog", () => ({
  ShareDialog: ({ open }: { open: boolean }) => (open ? <div>Share dialog</div> : null),
}));

import { useAdminCollection } from "@/api/admin/hooks";
const mockUseAdminCollection = vi.mocked(useAdminCollection);

function wrapper({ children }: { children: React.ReactNode }) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
}

const collection = {
  id: "col-agent-001",
  owner_id: "apikey:ingest",
  owner_email: "ingest@apikey.local",
  name: "Warehouse Onboarding Notes",
  description: "",
  thumbnail_s3_key: "",
  config: {},
  asset_tags: [],
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-06-01T00:00:00Z",
  sections: [
    {
      id: "sec-1",
      collection_id: "col-agent-001",
      title: "Schema Notes",
      description: "",
      position: 0,
      created_at: "2026-01-01T00:00:00Z",
      items: [
        {
          id: "itm-1",
          section_id: "sec-1",
          asset_id: "ast-006",
          position: 0,
          asset_name: "Data Quality Summary",
          asset_content_type: "text/markdown",
          asset_description: "",
          asset_thumbnail_s3_key: "thumbnails/ast-006.png",
          asset_thumbnail_dark_s3_key: "thumbnails/ast-006_dark.png",
          asset_thumbnail_version: 2,
          asset_thumbnail_dark_version: 2,
          created_at: "2026-01-01T00:00:00Z",
        },
      ],
    },
  ],
};

function mockCollection(value: unknown, isLoading = false) {
  mockUseAdminCollection.mockReturnValue({
    data: value,
    isLoading,
  } as unknown as ReturnType<typeof useAdminCollection>);
}

describe("AdminCollectionViewerPage (#1292)", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    updateState.isError = false;
  });

  it("names the owner of a collection the reader does not own", () => {
    mockCollection(collection);
    render(<AdminCollectionViewerPage collectionId="col-agent-001" onNavigate={vi.fn()} />, {
      wrapper,
    });

    expect(screen.getByText("Warehouse Onboarding Notes")).toBeInTheDocument();
    expect(screen.getByText(/ingest@apikey.local/)).toBeInTheDocument();
  });

  it("opens an item through the admin asset surface", () => {
    const onNavigate = vi.fn();
    mockCollection(collection);
    render(
      <AdminCollectionViewerPage collectionId="col-agent-001" onNavigate={onNavigate} />,
      { wrapper },
    );

    fireEvent.click(screen.getByText("Data Quality Summary"));
    // Not /collections/:id/assets/:id — the portal asset route is gated on
    // owning or being shared the asset, which is what the admin lacks here.
    expect(onNavigate).toHaveBeenCalledWith("/admin/assets/ast-006");
  });

  it("fetches item thumbnails from the admin route", () => {
    mockCollection(collection);
    const { container } = render(
      <AdminCollectionViewerPage collectionId="col-agent-001" onNavigate={vi.fn()} />,
      { wrapper },
    );

    const img = container.querySelector("img");
    // The capture's version rides along so a re-capture is a new URL rather
    // than an hour behind the browser cache (#1468).
    expect(img?.getAttribute("src")).toBe("/api/v1/admin/assets/ast-006/thumbnail?c=2");
  });

  it("saves an edited name through the admin route", async () => {
    mockCollection(collection);
    render(<AdminCollectionViewerPage collectionId="col-agent-001" onNavigate={vi.fn()} />, {
      wrapper,
    });

    fireEvent.click(screen.getByRole("button", { name: /edit details/i }));
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Warehouse Notes" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(updateMutate).toHaveBeenCalledWith({
        id: "col-agent-001",
        name: "Warehouse Notes",
        description: "",
      }),
    );
  });

  it("offers sharing, which is how an orphaned collection reaches its readers", () => {
    mockCollection(collection);
    render(<AdminCollectionViewerPage collectionId="col-agent-001" onNavigate={vi.fn()} />, {
      wrapper,
    });

    fireEvent.click(screen.getByRole("button", { name: /share/i }));
    expect(screen.getByText("Share dialog")).toBeInTheDocument();
  });

  it("keeps the form open and reports a refused save", async () => {
    mockCollection(collection);
    updateMutate.mockRejectedValueOnce(new Error("invalid name"));
    updateState.isError = true;
    render(<AdminCollectionViewerPage collectionId="col-agent-001" onNavigate={vi.fn()} />, {
      wrapper,
    });

    fireEvent.click(screen.getByRole("button", { name: /edit details/i }));
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(screen.getByText(/failed to save/i)).toBeInTheDocument());
    // The reader keeps their edit: closing the form would discard it.
    expect(screen.getByLabelText("Name")).toBeInTheDocument();
  });

  it("says so when the collection is gone", () => {
    mockCollection(undefined);
    render(<AdminCollectionViewerPage collectionId="missing" onNavigate={vi.fn()} />, {
      wrapper,
    });

    expect(screen.getByText("Collection not found")).toBeInTheDocument();
  });
});
