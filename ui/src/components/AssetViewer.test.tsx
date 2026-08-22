import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

import { AssetViewer } from "./AssetViewer";

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
  return render(
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
        {...props}
      />
    </QueryClientProvider>,
  );
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
