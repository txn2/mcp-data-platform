import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

// #2306: a scene is edited where it is drawn, and the page's own Save writes
// the version. The editor stands in for Excalidraw, which needs a canvas.
vi.mock("@/components/renderers/ExcalidrawRenderer", () => ({
  ExcalidrawRenderer: ({ onEdit }: { onEdit?: (s: string) => void }) =>
    onEdit ? (
      <button type="button" onClick={() => onEdit('{"type":"excalidraw","elements":[{"id":"new"}]}')}>
        draw
      </button>
    ) : (
      <p>read-only scene</p>
    ),
}));
vi.mock("@/components/SourceEditor", () => ({ SourceEditor: () => null }));
vi.mock("@/api/portal/hooks", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/portal/hooks")>();
  return { ...actual, useKnowledgeBacklinks: () => ({ data: { pages: [] } }) };
});

import { AssetViewer } from "./AssetViewer";

const scene = '{"type":"excalidraw","elements":[]}';
const asset = {
  id: "a1", owner_id: "owner", owner_email: "owner@example.com", name: "Sketch", description: "",
  content_type: "application/vnd.excalidraw+json", s3_bucket: "b", s3_key: "k", size_bytes: scene.length,
  tags: [], provenance: {}, current_version: 1, created_at: "2026-06-01T00:00:00Z", updated_at: "2026-06-01T00:00:00Z",
} as never;
const stub = () => ({ mutate: vi.fn(), mutateAsync: vi.fn(), isPending: false, isError: false });

function renderScene(contentUpdateMutation?: ReturnType<typeof stub>) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <AssetViewer
        asset={asset}
        content={scene}
        isLoading={false}
        contentUrl=""
        isOwner
        onBack={() => {}}
        onNavigate={() => {}}
        updateMutation={stub() as never}
        deleteMutation={stub() as never}
        contentUpdateMutation={contentUpdateMutation as never}
      />
    </QueryClientProvider>,
  );
}

describe("AssetViewer scene editing (#2306)", () => {
  it("offers Save in Preview once the drawing is edited, and saves the scene", async () => {
    const save = stub();
    renderScene(save);
    expect(screen.queryByRole("button", { name: /^save$/i })).toBeNull();
    fireEvent.click(await screen.findByRole("button", { name: "draw" }));
    fireEvent.click(screen.getByRole("button", { name: /^save$/i }));
    // The change summary dialog asks before writing the version.
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("button", { name: /^save$/i }));
    expect(save.mutate.mock.calls[0]![0]).toMatchObject({ id: "a1", content: '{"type":"excalidraw","elements":[{"id":"new"}]}' });
  });

  it("draws the scene read-only without edit access", async () => {
    renderScene(undefined);
    expect(await screen.findByText("read-only scene")).toBeInTheDocument();
  });
});
