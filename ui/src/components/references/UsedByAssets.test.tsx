import { describe, it, expect, vi, afterEach, beforeEach } from "vitest";
import { render, screen, cleanup, fireEvent, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReferencingAssetsResponse } from "@/api/portal/hooks/assetRefs";
import { UsedByAssets } from "./UsedByAssets";

// A managed resource could be edited or deleted with no way to find out what it
// was holding up (#1475), and so could an asset another asset reads from
// (#1488). What is asserted here is the section that answers both: it names the
// assets whose content references this thing, it flags the ones carrying a
// public link -- because a reference hands the target the asset's audience --
// and it counts the ones this reader may not open rather than leaving them out
// of the total.

const RESOURCE_ID = "res-logo";

const TWO_ASSETS: ReferencingAssetsResponse = {
  data: [
    { id: "asset-q4", name: "Q4 report", owner_email: "alex.rivera@example.com", public: false },
    { id: "asset-brief", name: "Board brief", owner_email: "alex.rivera@example.com", public: true },
  ],
  total: 2,
  hidden: 0,
};

function stubUsedBy(body: ReferencingAssetsResponse | { status: number }, path = `/resources/${RESOURCE_ID}/used-by`) {
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes(path)) {
        if ("status" in body) {
          return Promise.resolve(new Response(JSON.stringify({ detail: "no" }), { status: body.status }));
        }
        return Promise.resolve(new Response(JSON.stringify(body), { status: 200 }));
      }
      return Promise.reject(new Error(`unexpected request: ${url}`));
    }),
  );
}

function renderSection() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <UsedByAssets target={{ kind: "resource", id: RESOURCE_ID }} />
    </QueryClientProvider>,
  );
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("what a managed resource is holding up", () => {
  beforeEach(() => stubUsedBy(TWO_ASSETS));

  it("lists both assets and flags the publicly shared one", async () => {
    renderSection();

    expect(await screen.findByText("Q4 report")).toBeTruthy();
    expect(screen.getByText("Board brief")).toBeTruthy();
    // Exactly one flag: a reference carries the asset's audience, so only the
    // asset with a link share makes this file readable by anyone holding it.
    expect(screen.getAllByTestId("used-by-asset-public")).toHaveLength(1);
  });

  it("says what deleting the resource would do to them", async () => {
    renderSection();
    expect(await screen.findByText(/rendering without it/)).toBeTruthy();
  });

  it("counts an asset this reader cannot open without naming it", async () => {
    stubUsedBy({ data: [TWO_ASSETS.data[0]!], total: 1, hidden: 2 });
    renderSection();

    expect(await screen.findByTestId("used-by-assets-hidden")).toBeTruthy();
    // The heading counts what is there, not only what is listed: someone about
    // to delete the file has to know the list is not the whole of it.
    expect(screen.getByText(/Used by 3 assets/)).toBeTruthy();
  });

  it("shows nothing when no asset references the file", async () => {
    stubUsedBy({ data: [], total: 0, hidden: 0 });
    const { container } = renderSection();

    await waitFor(() =>
      expect(container.querySelector("[data-testid='used-by-assets']")).toBeNull(),
    );
    // The control: the same stub renders the section when there is something to
    // report, so an absent section here is the empty answer and not a component
    // that failed to mount.
    cleanup();
    stubUsedBy(TWO_ASSETS);
    renderSection();
    expect(await screen.findByTestId("used-by-assets")).toBeTruthy();
  });

  it("shows nothing when the list cannot be read", async () => {
    stubUsedBy({ status: 500 });
    const { container } = renderSection();

    await waitFor(() =>
      expect(container.querySelector("[data-testid='used-by-assets']")).toBeNull(),
    );
  });
});

describe("a bounded answer", () => {
  it("reads as a floor rather than a total", async () => {
    stubUsedBy({ data: TWO_ASSETS.data, total: 2, hidden: 0, truncated: true });
    renderSection();

    // "Used by 2 assets" on a list the server cut would say the file is holding
    // up two things when it is holding up more.
    expect(await screen.findByText(/Used by at least/)).toBeTruthy();
  });
});

// The same section, asked about an asset (#1488). It is one component over two
// kinds, so what differs is the route it reads and the noun it uses -- and both
// have to be right, or the section on the asset viewer would ask the resources
// route about an asset id and answer "nothing uses this".
describe("what an asset is holding up", () => {
  const ASSET_ID = "asset-data";

  it("reads the asset's own used-by route and names the assets reading it", async () => {
    stubUsedBy(TWO_ASSETS, `/assets/${ASSET_ID}/used-by`);
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={qc}>
        <UsedByAssets target={{ kind: "asset", id: ASSET_ID }} />
      </QueryClientProvider>,
    );

    expect(await screen.findByText("Q4 report")).toBeTruthy();
    expect(screen.getByTestId("used-by-assets").textContent).toContain(
      "Deleting this asset leaves those assets rendering without it",
    );
  });

  it("opens a referencing asset where this reader can", async () => {
    stubUsedBy(TWO_ASSETS, `/assets/${ASSET_ID}/used-by`);
    const onNavigate = vi.fn();
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={qc}>
        <UsedByAssets
          target={{ kind: "asset", id: ASSET_ID }}
          assetPath={(id) => `/assets/${id}`}
          onNavigate={onNavigate}
        />
      </QueryClientProvider>,
    );

    fireEvent.click(await screen.findByText("Q4 report"));
    expect(onNavigate).toHaveBeenCalledWith("/assets/asset-q4");
  });
});
