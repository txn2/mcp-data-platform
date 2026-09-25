import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

import { ContentFetchError } from "@/lib/contentFetch";
import { AssetViewer } from "./AssetViewer";

// One asset is referenced by a knowledge page; every other asset here is not,
// so the rest of the file renders the viewer it always has.
vi.mock("@/api/portal/hooks", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/portal/hooks")>();
  return {
    ...actual,
    useKnowledgeBacklinks: (urn?: string) => ({
      data: {
        pages: urn === "mcp:asset:cited" ? [{ id: "kp1", slug: "slides", title: "Building slides" }] : [],
      },
    }),
  };
});

const stubMutation = () => ({ mutate: vi.fn(), mutateAsync: vi.fn(), isPending: false, isError: false }) as never;

function markdownAsset(overrides: Record<string, unknown> = {}) {
  return {
    id: "a1",
    owner_id: "owner",
    owner_email: "owner@example.com",
    name: "Notes",
    description: "",
    content_type: "text/markdown",
    s3_bucket: "b",
    s3_key: "k",
    size_bytes: 4,
    tags: [],
    provenance: {},
    current_version: 1,
    created_at: "2026-06-01T00:00:00Z",
    updated_at: "2026-06-01T00:00:00Z",
    ...overrides,
  } as never;
}

function renderViewer(props: Record<string, unknown>) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const tree = (p: Record<string, unknown>) => (
    <QueryClientProvider client={qc}>
      <AssetViewer
        asset={markdownAsset()}
        content={"# hi"}
        isLoading={false}
        contentUrl=""
        onBack={() => {}}
        onNavigate={() => {}}
        updateMutation={stubMutation()}
        deleteMutation={stubMutation()}
        {...p}
      />
    </QueryClientProvider>
  );
  const result = render(tree(props));
  // Rerenders the same viewer with different props, which is what following a
  // link to another asset does: the page that renders this is not keyed by
  // asset, so the instance is reused.
  return { ...result, rerender: (p: Record<string, unknown>) => result.rerender(tree(p)) };
}

describe("AssetViewer metadata edit affordance (#611)", () => {
  it("shows the Edit button to a shared editor", () => {
    renderViewer({ isOwner: false, sharePermission: "editor" });
    fireEvent.click(screen.getByTitle("Show details"));
    expect(screen.getByTitle("Edit")).toBeInTheDocument();
  });

  it("hides the Edit button from a shared viewer", () => {
    renderViewer({ isOwner: false, sharePermission: "viewer" });
    fireEvent.click(screen.getByTitle("Show details"));
    expect(screen.queryByTitle("Edit")).not.toBeInTheDocument();
  });

  it("shows the Edit button to the owner", () => {
    renderViewer({ isOwner: true });
    fireEvent.click(screen.getByTitle("Show details"));
    expect(screen.getByTitle("Edit")).toBeInTheDocument();
  });
});

// The retention control's trip through the real viewer (#1421): what the asset
// stores decides the mode the form opens in, and what the person picks is what
// the update carries. A control that renders correctly but sends nothing is the
// failure this covers.
describe("AssetViewer version retention (#1421)", () => {
  function openEditor(props: Record<string, unknown>) {
    const updateMutation = { mutate: vi.fn(), mutateAsync: vi.fn(), isPending: false, isError: false };
    renderViewer({ isOwner: true, updateMutation: updateMutation as never, ...props });
    fireEvent.click(screen.getByTitle("Show details"));
    fireEvent.click(screen.getByTitle("Edit"));
    return updateMutation;
  }

  it("leaves retention out of an update that did not move it", () => {
    // The API reserves the field to the owner and an admin, so a save that only
    // renamed the asset must not restate a setting nobody touched.
    const updateMutation = openEditor({ asset: markdownAsset({ max_versions: 25 }) });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    const sent = updateMutation.mutate.mock.calls[0]![0] as Record<string, unknown>;
    expect(sent).not.toHaveProperty("max_versions");
  });

  it("hides the control from an editor-share recipient", () => {
    // An editor may change everything else about the asset; how much of the
    // owner's history survives the next write is not theirs to decide.
    renderViewer({ isOwner: false, sharePermission: "editor" });
    fireEvent.click(screen.getByTitle("Show details"));
    fireEvent.click(screen.getByTitle("Edit"));
    expect(screen.queryByLabelText("Version history")).toBeNull();
    expect(screen.getByRole("button", { name: "Save" })).toBeEnabled();
  });

  it("opens on the deployment default when the asset carries no override", () => {
    openEditor({});
    expect(screen.getByLabelText("Version history")).toHaveTextContent("Deployment default");
    expect(screen.queryByLabelText("Versions to keep")).toBeNull();
  });

  it("sends null when an asset with a cap goes back to the deployment default", () => {
    const updateMutation = openEditor({ asset: markdownAsset({ max_versions: 25 }) });
    fireEvent.click(screen.getByLabelText("Version history"));
    fireEvent.click(screen.getByRole("option", { name: "Deployment default" }));
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    expect(updateMutation.mutate).toHaveBeenCalledWith(
      expect.objectContaining({ id: "a1", max_versions: null }),
      expect.anything(),
    );
  });

  it("seeds the count from the asset's own cap and sends it back", () => {
    const updateMutation = openEditor({ asset: markdownAsset({ max_versions: 25 }) });
    expect(screen.getByLabelText("Versions to keep")).toHaveValue(25);

    fireEvent.change(screen.getByLabelText("Versions to keep"), { target: { value: "40" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(updateMutation.mutate).toHaveBeenCalledWith(
      expect.objectContaining({ max_versions: 40 }),
      expect.anything(),
    );
  });

  it("refuses to save a cap nobody finished typing", () => {
    // Blanking the box and saving must not resolve to 1 (keep almost nothing)
    // or 0 (keep everything). Neither is what the person asked for.
    const updateMutation = openEditor({ asset: markdownAsset({ max_versions: 25 }) });
    fireEvent.change(screen.getByLabelText("Versions to keep"), { target: { value: "" } });

    const save = screen.getByRole("button", { name: "Save" });
    expect(save).toBeDisabled();
    fireEvent.click(save);
    expect(updateMutation.mutate).not.toHaveBeenCalled();
  });

  it("opens on unlimited for an asset that keeps every version", () => {
    openEditor({ asset: markdownAsset({ max_versions: 0 }) });
    expect(screen.getByLabelText("Version history")).toHaveTextContent("Keep every version");
    expect(screen.queryByLabelText("Versions to keep")).toBeNull();
  });

  it("sends 0 when an asset is switched to keeping every version", () => {
    const updateMutation = openEditor({ asset: markdownAsset({ max_versions: 25 }) });
    fireEvent.click(screen.getByLabelText("Version history"));
    fireEvent.click(screen.getByRole("option", { name: "Keep every version" }));
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    expect(updateMutation.mutate).toHaveBeenCalledWith(
      expect.objectContaining({ max_versions: 0 }),
      expect.anything(),
    );
  });
});

// Tiles are drawn by the platform's renderer (#1787). Opening an asset whose
// tile is behind draws nothing in the reader's tab; the owner's panel says the
// tile is being drawn and asks the server for another on a press.
describe("AssetViewer thumbnail", () => {
  const cleared = {
    thumbnail_s3_key: "",
    thumbnail_dark_s3_key: "",
    thumbnail_version: 0,
    thumbnail_dark_version: 0,
    current_version: 4,
  };

  it("draws nothing in the reader's tab and says the tile is being drawn", async () => {
    renderViewer({ asset: markdownAsset(cleared) });
    fireEvent.click(screen.getByTitle("Show details"));
    expect(await screen.findByText("Being drawn")).toBeInTheDocument();
    expect(document.querySelector("iframe[title='Thumbnail capture']")).toBeNull();
  });

  // A refused clear discarded nothing, and saying so is the only way the owner
  // learns their press did not take.
  it("reports a clear the server refused", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn((_input: RequestInfo | URL, init?: RequestInit) =>
        init?.method === "DELETE"
          ? Promise.resolve(new Response(JSON.stringify({ detail: "refused" }), { status: 403 }))
          : Promise.reject(new Error("not stubbed")),
      ),
    );
    try {
      // A tile that has been drawn: one being drawn offers no press (#1791).
      renderViewer({
        asset: markdownAsset({
          ...cleared,
          thumbnail_s3_key: "k/.thumbnail.png",
          thumbnail_dark_s3_key: "k/.thumbnail_dark.png",
          thumbnail_version: 4,
          thumbnail_dark_version: 4,
        }),
      });
      fireEvent.click(screen.getByTitle("Show details"));
      fireEvent.click(screen.getByTitle("Discard this image and draw it again"));
      expect(await screen.findByText("Could not discard the stored image.")).toBeInTheDocument();
    } finally {
      vi.unstubAllGlobals();
    }
  });
});

// The asset viewer has little vertical room, and a card above the toolbar
// naming one page pushed the document down by the toolbar's height (#1792).
describe("AssetViewer knowledge page references", () => {
  const versions = [1, 2].map((n) => ({
    id: `ver-${n}`,
    asset_id: "cited",
    version: n,
    s3_key: `k${n}`,
    s3_bucket: "b",
    content_type: "text/markdown",
    size_bytes: 4,
    created_by: "owner",
    change_summary: "",
    created_at: "2026-06-01T00:00:00Z",
  }));

  it("names them on a button to the right of the version selector, not in a card", () => {
    renderViewer({
      asset: markdownAsset({ id: "cited", current_version: 2 }),
      versions,
      onSelectVersion: vi.fn(),
    });
    const selector = screen.getByRole("combobox", { name: "Asset version" });
    const refs = screen.getByRole("button", { name: /referenced by/i });
    expect(selector.nextElementSibling).toBe(refs);
    expect(screen.queryByText("1 knowledge page references this")).not.toBeInTheDocument();
    expect(screen.queryByText("Building slides")).not.toBeInTheDocument();
  });

  it("shows no button for an asset no page references", () => {
    renderViewer({ versions, onSelectVersion: vi.fn() });
    expect(screen.queryByRole("button", { name: /referenced by/i })).not.toBeInTheDocument();
  });
});

describe("AssetViewer content that failed to load (#1874)", () => {
  it("names the status with Retry and Download instead of the loading indicator", () => {
    const onRetry = vi.fn();
    renderViewer({
      asset: markdownAsset({ content_type: "text/csv", name: "d.csv", size_bytes: 5 * 1024 * 1024 }),
      content: undefined,
      contentError: new ContentFetchError(403),
      onRetryContent: onRetry,
      contentUrl: "/api/v1/portal/assets/a1/content",
    });
    expect(screen.getByText("Could not load this file")).toBeInTheDocument();
    expect(screen.getByText(/HTTP 403/)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /Download/ })).toHaveAttribute("href", "/api/v1/portal/assets/a1/content");
    fireEvent.click(screen.getByRole("button", { name: /Retry/ }));
    expect(onRetry).toHaveBeenCalledTimes(1);
  });

  it("shows a CSV past its family's limit as too large, not loading", () => {
    renderViewer({
      asset: markdownAsset({ content_type: "text/csv", name: "d.csv", size_bytes: 40 * 1024 * 1024 }),
      content: undefined,
      contentUrl: "/api/v1/portal/assets/a1/content",
    });
    expect(screen.getByText("Too large to preview")).toBeInTheDocument();
  });
});
