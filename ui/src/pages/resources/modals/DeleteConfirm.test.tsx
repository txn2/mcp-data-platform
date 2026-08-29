import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen, cleanup, fireEvent, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { Resource } from "@/api/resources/types";
import type { ReferencingAssetsResponse } from "@/api/portal/hooks/assetRefs";
import { DeleteConfirm } from "./DeleteConfirm";

// Deleting a managed resource leaves every asset referencing it rendering
// without that file, and the reference row survives the delete so the record of
// the break is not erased (#1474). What is asserted here is that the dialog says
// so, and names them, before the delete rather than after it (#1475).

const RESOURCE: Resource = {
  id: "res-logo",
  scope: "global",
  scope_id: "",
  path: "brand",
  filename: "logo.png",
  display_name: "Company logo",
  description: "",
  mime_type: "image/png",
  size_bytes: 4096,
  s3_key: "resources/global/brand/logo.png",
  uri: "mcp://global/brand/logo.png",
  tags: [],
  uploader_sub: "alex",
  uploader_email: "alex.rivera@example.com",
  created_at: "2026-08-01T00:00:00Z",
  updated_at: "2026-08-01T00:00:00Z",
};

let deleted = false;

function stubApi(usedBy: ReferencingAssetsResponse) {
  deleted = false;
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes("/used-by")) {
        return Promise.resolve(new Response(JSON.stringify(usedBy), { status: 200 }));
      }
      if (init?.method === "DELETE") {
        deleted = true;
        return Promise.resolve(new Response(JSON.stringify({ status: "deleted" }), { status: 200 }));
      }
      return Promise.reject(new Error(`unexpected request: ${url}`));
    }),
  );
}

function renderDialog() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <DeleteConfirm resource={RESOURCE} onClose={() => {}} />
    </QueryClientProvider>,
  );
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("deleting a resource assets reference", () => {
  it("names the assets and flags the publicly shared one", async () => {
    stubApi({
      data: [
        { id: "asset-q4", name: "Q4 report", public: false },
        { id: "asset-brief", name: "Board brief", public: true },
      ],
      total: 2,
      hidden: 0,
    });
    renderDialog();

    const warning = await screen.findByTestId("delete-resource-used-by-assets");
    expect(warning.textContent).toContain("Q4 report");
    expect(warning.textContent).toContain("Board brief");
    expect(warning.textContent).toContain("public link");
    expect(deleted).toBe(false);
  });

  it("counts the assets this reader cannot open", async () => {
    stubApi({ data: [], total: 0, hidden: 2 });
    renderDialog();

    const warning = await screen.findByTestId("delete-resource-used-by-assets");
    expect(warning.textContent).toContain("2 assets reference");
    expect(warning.textContent).toContain("cannot open");
  });

  it("warns and still deletes on confirmation", async () => {
    stubApi({ data: [{ id: "asset-q4", name: "Q4 report", public: false }], total: 1, hidden: 0 });
    renderDialog();

    await screen.findByTestId("delete-resource-used-by-assets");
    fireEvent.click(screen.getByText("Delete"));
    await waitFor(() => expect(deleted).toBe(true));
  });

  it("says nothing about assets when none reference the file", async () => {
    stubApi({ data: [], total: 0, hidden: 0 });
    const { container } = renderDialog();

    // The prompt itself is there, so an absent warning is the empty answer
    // rather than a dialog that failed to render.
    expect(await screen.findByText(/Are you sure you want to delete/)).toBeTruthy();
    await waitFor(() =>
      expect(container.querySelector("[data-testid='delete-resource-used-by-assets']")).toBeNull(),
    );
  });
});

describe("the delete is not armed before the check answers", () => {
  it("holds Delete disabled while the check is in flight", async () => {
    // A request that never resolves: the button must stay disabled rather than
    // letting the fastest click skip the warning the dialog exists for.
    vi.stubGlobal("fetch", vi.fn(() => new Promise(() => {})));
    renderDialog();

    const button = await screen.findByRole("button", { name: /Delete/ });
    expect((button as HTMLButtonElement).disabled).toBe(true);
  });

  it("says so when the check itself fails, rather than staying silent", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL) => {
        if (String(input).includes("/used-by")) {
          return Promise.resolve(new Response(JSON.stringify({ detail: "no" }), { status: 500 }));
        }
        return Promise.reject(new Error("unexpected"));
      }),
    );
    renderDialog();

    expect(await screen.findByTestId("delete-resource-check-failed")).toBeTruthy();
    const button = await screen.findByRole("button", { name: /Delete/ });
    expect((button as HTMLButtonElement).disabled).toBe(false);
  });

  it("reads a bounded answer as a floor, not a total", async () => {
    stubApi({
      data: [{ id: "asset-q4", name: "Q4 report", public: false }],
      total: 1,
      hidden: 0,
      truncated: true,
    });
    renderDialog();

    const warning = await screen.findByTestId("delete-resource-used-by-assets");
    expect(warning.textContent).toContain("At least 1 asset references");
  });
});
