import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen, cleanup, fireEvent, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { CollectionTarget } from "@/api/portal/types";
import { AddToCollectionDialog, AddedToast, sectionLabel } from "./AddToCollectionDialog";

// Putting an asset into a collection from the viewer (#2260): the picker lists
// the collections the reader can edit, marks the ones already holding the
// asset, asks for the section only when there is more than one, refuses the
// section the asset is already in, and offers New collection to a persona that
// creates collections.

const ASSET = "asset-q4";

const TARGETS: CollectionTarget[] = [
  {
    id: "col-board",
    name: "Board pack",
    updated_at: "2026-10-20T00:00:00Z",
    shared: false,
    sections: [
      { id: "s-sum", title: "Summary", item_count: 2, has_asset: true },
      { id: "s-app", title: "", item_count: 4, has_asset: false },
      { id: "s-last", title: "Appendix", item_count: 1, has_asset: false },
    ],
  },
  {
    id: "col-one",
    name: "Weekly",
    owner_email: "sam.chen@example.com",
    updated_at: "2026-10-19T00:00:00Z",
    shared: true,
    sections: [{ id: "s-only", title: "Charts", item_count: 3, has_asset: false }],
  },
  {
    id: "col-full",
    name: "Already filed",
    updated_at: "2026-10-18T00:00:00Z",
    shared: false,
    sections: [{ id: "s-f", title: "Main", item_count: 1, has_asset: true }],
  },
];

let calls: { url: string; method: string; body?: string }[] = [];

function stubApi(targets: CollectionTarget[] = TARGETS, appendStatus = 201) {
  calls = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";
      calls.push({ url, method, body: init?.body as string });
      const json = (body: unknown, status = 200) =>
        Promise.resolve(new Response(JSON.stringify(body), { status }));
      if (url.includes("/collection-targets")) return json({ data: targets });
      if (url.endsWith("/items") && method === "POST") {
        if (appendStatus !== 201) return json({ detail: "the asset is already in this section" }, appendStatus);
        const sent = JSON.parse(init?.body as string) as { section_id?: string };
        return json({ item: { id: "i" }, section_id: sent.section_id ?? "s-only", section_title: "Charts" }, 201);
      }
      if (url.endsWith("/collections") && method === "POST") {
        return json({ id: "col-new", name: "Q4 review" }, 201);
      }
      return Promise.reject(new Error(`unexpected ${method} ${url}`));
    }),
  );
}

function renderDialog(canCreate = true) {
  const onAdded = vi.fn();
  const onOpenChange = vi.fn();
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <AddToCollectionDialog assetId={ASSET} open canCreate={canCreate} onOpenChange={onOpenChange} onAdded={onAdded} />
    </QueryClientProvider>,
  );
  return { onAdded, onOpenChange };
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("AddToCollectionDialog", () => {
  it("lists editable collections and marks where the asset already is", async () => {
    stubApi();
    renderDialog();
    expect(await screen.findByText("Board pack")).toBeTruthy();
    expect(screen.getByText("Already in Summary")).toBeTruthy();
    expect(screen.getByText("Owned by sam.chen@example.com")).toBeTruthy();
    // A collection whose every section holds the asset cannot be chosen.
    expect(screen.getByText("Already filed").closest("button")?.disabled).toBe(true);
  });

  it("adds straight away to a collection with one section", async () => {
    stubApi();
    const { onAdded, onOpenChange } = renderDialog();
    fireEvent.click(await screen.findByText("Weekly"));
    await waitFor(() => expect(onAdded).toHaveBeenCalled());
    const post = calls.find((c) => c.method === "POST");
    expect(post?.url).toContain("/collections/col-one/items");
    expect(JSON.parse(post!.body!)).toEqual({ asset_id: ASSET, section_id: "s-only" });
    expect(onAdded).toHaveBeenCalledWith({ collectionId: "col-one", collectionName: "Weekly", sectionTitle: "Charts" });
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it("asks for the section when there are several, defaulting to the last open one", async () => {
    stubApi();
    const { onAdded } = renderDialog();
    fireEvent.click(await screen.findByText("Board pack"));
    expect(await screen.findByText("Choose a section")).toBeTruthy();
    const radios = screen.getAllByRole("radio") as HTMLInputElement[];
    expect(radios.map((r) => r.disabled)).toEqual([true, false, false]);
    expect(radios[2]!.checked).toBe(true);
    expect(screen.getByText("Untitled section")).toBeTruthy();
    fireEvent.click(radios[1]!);
    fireEvent.click(screen.getByRole("button", { name: "Add" }));
    await waitFor(() => expect(onAdded).toHaveBeenCalled());
    const post = calls.find((c) => c.method === "POST");
    expect(JSON.parse(post!.body!)).toEqual({ asset_id: ASSET, section_id: "s-app" });
  });

  it("shows the server's refusal", async () => {
    stubApi(TARGETS, 409);
    const { onAdded } = renderDialog();
    fireEvent.click(await screen.findByText("Weekly"));
    expect((await screen.findByRole("alert")).textContent).toContain("already in this section");
    expect(onAdded).not.toHaveBeenCalled();
  });

  it("creates a new collection with the asset in it", async () => {
    stubApi();
    const { onAdded } = renderDialog();
    fireEvent.click(await screen.findByRole("button", { name: /New collection/ }));
    fireEvent.change(screen.getByLabelText("Collection name"), { target: { value: "  Q4 review " } });
    fireEvent.click(screen.getByRole("button", { name: "Create and add" }));
    await waitFor(() => expect(onAdded).toHaveBeenCalled());
    const post = calls.find((c) => c.method === "POST");
    expect(post?.url).toMatch(/\/collections$/);
    expect(JSON.parse(post!.body!)).toEqual({ name: "Q4 review", asset_id: ASSET });
    expect(onAdded).toHaveBeenCalledWith({ collectionId: "col-new", collectionName: "Q4 review", sectionTitle: "" });
  });

  it("offers no New collection to a persona that cannot create one", async () => {
    stubApi();
    renderDialog(false);
    await screen.findByText("Board pack");
    expect(screen.queryByRole("button", { name: /New collection/ })).toBeNull();
  });

  it("searches on the server and says when nothing matches", async () => {
    stubApi([]);
    renderDialog();
    fireEvent.change(await screen.findByLabelText("Search collections"), { target: { value: "zzz" } });
    expect(await screen.findByText("No collection you can edit matches.")).toBeTruthy();
    expect(calls.some((c) => c.url.includes("collection-targets?search=zzz"))).toBe(true);
  });
});

describe("AddedToast", () => {
  it("names the collection and section and opens the collection", () => {
    const onOpen = vi.fn();
    const onDismiss = vi.fn();
    render(
      <AddedToast
        added={{ collectionId: "col-one", collectionName: "Weekly", sectionTitle: "Charts" }}
        onOpen={onOpen}
        onDismiss={onDismiss}
      />,
    );
    expect(screen.getByTestId("added-to-collection").textContent).toContain("Added to Weekly / Charts");
    fireEvent.click(screen.getByRole("button", { name: "Open" }));
    expect(onOpen).toHaveBeenCalledWith("col-one");
    expect(onDismiss).toHaveBeenCalled();
  });

  it("names only the collection for an untitled section", () => {
    render(<AddedToast added={{ collectionId: "c", collectionName: "Weekly", sectionTitle: "" }} onOpen={vi.fn()} onDismiss={vi.fn()} />);
    expect(screen.getByTestId("added-to-collection").textContent).toContain("Added to Weekly");
    expect(screen.getByTestId("added-to-collection").textContent).not.toContain("/");
  });

  it("renders nothing with nothing added", () => {
    render(<AddedToast added={null} onOpen={vi.fn()} onDismiss={vi.fn()} />);
    expect(screen.queryByTestId("added-to-collection")).toBeNull();
  });
});

describe("sectionLabel", () => {
  it("names an untitled section", () => {
    expect(sectionLabel("  ")).toBe("Untitled section");
    expect(sectionLabel("Charts")).toBe("Charts");
  });
});
