import { describe, it, expect, vi, afterEach, beforeEach } from "vitest";
import { render, screen, cleanup, fireEvent, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { AssetRefsResponse } from "@/api/portal/hooks/assetRefs";
import { ReferencesPanel } from "./ReferencesPanel";

// An asset's references were invisible to the person who owned either end
// (#1475): the mechanism let an agent declare one at save time and let every
// viewing surface resolve it, and gave nobody a way to see or act on it. What
// is asserted here is the panel that closes that: it lists what the asset
// depends on -- uploaded files and other assets alike (#1488) -- it links and
// pictures each one, it states what adding one gives away before it is
// confirmed, it warns with the lines the content still writes a reference on
// before removing it, and it offers a reader without edit authority nothing to
// press.

const ASSET_ID = "asset-q4";
const LOGO_URI = "mcp://global/brand/logo.png";
const REF_URL = "/portal/refs/asset-q4/tok-logo";
const NOTICE =
  "Anyone this asset is shared with can load these files through it, including anyone holding a public link, now and later.";

const WITH_LOGO: AssetRefsResponse = {
  data: [
    {
      target_kind: "resource",
      target_id: "res-logo",
      uri: LOGO_URI,
      position: 0,
      declared_by: "alex.rivera@example.com",
      display_name: "Company logo",
      filename: "logo.png",
      mime_type: "image/png",
      size_bytes: 4096,
      scope: "global",
      content_url: REF_URL,
      readable: true,
      occurrences: [{ line: 2, snippet: '<img src="' + LOGO_URI + '">' }],
    },
  ],
  total: 1,
  audience: { public: false, shared_with_users: false },
  can_edit: true,
  max: 20,
  notice: NOTICE,
  content_scanned: true,
};

// clone keeps each test's fixture its own, so a test that edits one field does
// not reach the next through the shared literal.
function clone(base: AssetRefsResponse): AssetRefsResponse {
  return JSON.parse(JSON.stringify(base)) as AssetRefsResponse;
}

const EMPTY: AssetRefsResponse = {
  data: [],
  total: 0,
  audience: { public: false, shared_with_users: false },
  can_edit: true,
  max: 20,
  notice: NOTICE,
  content_scanned: true,
};

// sharedAsset is one asset row as the list routes return it.
function sharedAsset(id: string, name: string) {
  return {
    id,
    name,
    content_type: "text/csv",
    owner_email: "sam.chen@example.com",
    size_bytes: 120,
    tags: [],
    created_at: "2026-08-01T00:00:00Z",
    updated_at: "2026-08-01T00:00:00Z",
  };
}

// calls records every request the panel makes, so a test can assert on the
// write it produced rather than only on what re-rendered.
let calls: { url: string; method: string; body?: string }[] = [];

// stubApi answers the reference list and the resource library. Anything else
// rejects, so a route the panel starts using shows up as a failure rather than
// as a silently empty panel.
function stubApi(
  refs: AssetRefsResponse | { status: number },
  library: {
    total?: number;
    assetTotal?: number;
    sharedTotal?: number;
    /** Put one asset in the owned list AND the shared list. */
    bothLists?: boolean;
  } = {},
) {
  calls = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      calls.push({ url, method: init?.method ?? "GET", body: init?.body as string });
      const json = (body: unknown, status = 200) =>
        Promise.resolve(new Response(JSON.stringify(body), { status }));

      if (url.includes("/api/v1/resources")) {
        return json({
          resources: [
            {
              id: "res-chart",
              scope: "global",
              scope_id: "",
              category: "brand",
              filename: "chart.png",
              display_name: "Revenue chart",
              description: "",
              mime_type: "image/png",
              size_bytes: 8192,
              s3_key: "k",
              uri: "mcp://global/brand/chart.png",
              tags: [],
              uploader_sub: "u",
              uploader_email: "u@example.com",
              created_at: "2026-08-01T00:00:00Z",
              updated_at: "2026-08-01T00:00:00Z",
            },
          ],
          total: library.total ?? 1,
        });
      }
      if (url.includes("/references")) {
        if ("status" in refs) return json({ detail: "nope" }, refs.status);
        return json(refs);
      }
      // The ranked asset search the picker reads once the author types. It
      // answers with scored wrappers rather than assets, which is a different
      // shape from the list beside it.
      if (url.includes("/api/v1/portal/assets/search")) {
        return json({
          data: [{ asset: sharedAsset("asset-shared", "Regional rollup"), score: 0.9 }],
          total: 1,
        });
      }
      // The assets other people shared with this reader: the add accepts one,
      // so the picker has to offer it.
      if (url.includes("/api/v1/portal/shared-with-me")) {
        return json({
          data: [
            ...(library.bothLists
              ? [
                  {
                    asset: sharedAsset("asset-data", "Weekly numbers"),
                    share_id: "s0",
                    shared_by: "sam.chen@example.com",
                    shared_at: "2026-08-01T00:00:00Z",
                    permission: "viewer",
                  },
                ]
              : []),
            {
              asset: sharedAsset("asset-shared", "Regional rollup"),
              share_id: "s1",
              shared_by: "sam.chen@example.com",
              shared_at: "2026-08-01T00:00:00Z",
              permission: "viewer",
            },
          ],
          total: library.sharedTotal ?? 1,
        });
      }
      // The asset library the picker's second tab reads (#1488).
      if (url.includes("/api/v1/portal/assets")) {
        return json({
          data: [
            {
              id: "asset-data",
              name: "Weekly numbers",
              content_type: "text/csv",
              owner_email: "alex.rivera@example.com",
              size_bytes: 120,
              tags: [],
              created_at: "2026-08-01T00:00:00Z",
              updated_at: "2026-08-01T00:00:00Z",
            },
          ],
          total: library.assetTotal ?? 1,
        });
      }
      return Promise.reject(new Error(`unexpected request: ${url}`));
    }),
  );
}

function renderPanel(props: Partial<React.ComponentProps<typeof ReferencesPanel>> = {}) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <ReferencesPanel assetId={ASSET_ID} {...props} />
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  stubApi(WITH_LOGO);
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("an asset's referenced files have a surface", () => {
  it("lists the file with its scope, type and picture", async () => {
    renderPanel();

    expect(await screen.findByText("Company logo")).toBeTruthy();
    expect(screen.getByText("global")).toBeTruthy();
    expect(screen.getByText("image/png")).toBeTruthy();

    const thumb = screen.getByTestId("asset-ref-thumb") as HTMLImageElement;
    // The picture loads through the reference's own URL, which is the grant the
    // asset already makes: the resource route would refuse a reader who was
    // only ever shown the asset.
    expect(thumb.getAttribute("src")).toBe(REF_URL);
  });

  it("carries the URI the content has to name, with a copy control", async () => {
    renderPanel();
    expect(await screen.findByText(LOGO_URI)).toBeTruthy();
    expect(screen.getByLabelText("Copy reference")).toBeTruthy();
  });

  it("opens the file where this reader can read it on their own", async () => {
    const onNavigate = vi.fn();
    renderPanel({ resourcePath: (id) => `/resources/${id}`, onNavigate });

    fireEvent.click(await screen.findByText("Company logo"));
    expect(onNavigate).toHaveBeenCalledWith("/resources/res-logo");
  });

  it("names a file this reader cannot open without linking to it", async () => {
    const refs = clone(WITH_LOGO);
    refs.data[0]!.readable = false;
    stubApi(refs);
    const onNavigate = vi.fn();
    renderPanel({ resourcePath: (id) => `/resources/${id}`, onNavigate });

    const name = await screen.findByText("Company logo");
    fireEvent.click(name);
    expect(onNavigate).not.toHaveBeenCalled();
  });

  it("flags a reference whose file was deleted rather than dropping the row", async () => {
    const refs = clone(WITH_LOGO);
    refs.data[0] = {
      target_kind: "resource",
      target_id: "res-logo",
      uri: LOGO_URI,
      position: 0,
      broken: true,
      occurrences: [{ line: 2, snippet: "x" }],
    };
    stubApi(refs);
    renderPanel();

    expect(await screen.findByTestId("asset-ref-broken")).toBeTruthy();
    expect(screen.getByText(LOGO_URI)).toBeTruthy();
    expect(screen.queryByTestId("asset-ref-thumb")).toBeNull();
  });

  it("says where the content writes the URI", async () => {
    renderPanel();
    expect(await screen.findByTestId("asset-ref-in-content")).toBeTruthy();
  });

  // The positive control for the two "shows nothing" cases below: the same
  // empty list, read through the same stub, does render for someone who can act
  // on it. Without it, an absent panel would pass those tests for any reason at
  // all, including a component that never rendered.
  it("offers an owner with no references a way to add the first one", async () => {
    stubApi(EMPTY);
    renderPanel();

    expect(await screen.findByText(/Reference a logo/)).toBeTruthy();
    expect(screen.getByTestId("asset-refs")).toBeTruthy();
    expect(screen.getByText("Add")).toBeTruthy();
  });

  it("shows nothing at all when there are no references and the reader cannot add one", async () => {
    const refs = clone(EMPTY);
    refs.can_edit = false;
    stubApi(refs);
    const { container } = renderPanel();

    // The stub is the one the control above renders through, so an absent
    // panel here is the reader's authority and not a component that never got
    // its answer.
    await waitFor(() => expect(calls.some((c) => c.url.includes("/references"))).toBe(true));
    await waitFor(() =>
      expect(container.querySelector("[data-testid='asset-refs']")).toBeNull(),
    );
  });

  it("shows nothing when the list cannot be read", async () => {
    stubApi({ status: 500 });
    const { container } = renderPanel();

    await waitFor(() => expect(calls.some((c) => c.url.includes("/references"))).toBe(true));
    await waitFor(() =>
      expect(container.querySelector("[data-testid='asset-refs']")).toBeNull(),
    );
  });

  it("offers a reader without edit authority the list and no controls", async () => {
    const refs = clone(WITH_LOGO);
    refs.can_edit = false;
    stubApi(refs);
    renderPanel();

    expect(await screen.findByText("Company logo")).toBeTruthy();
    expect(screen.queryByText("Add")).toBeNull();
    expect(screen.queryByLabelText("Remove Company logo")).toBeNull();
  });
});

describe("adding a reference", () => {
  it("names what the reference gives away, and this asset's current audience", async () => {
    const refs = clone(WITH_LOGO);
    refs.audience = { public: true, shared_with_users: false };
    stubApi(refs);
    renderPanel();

    fireEvent.click(await screen.findByText("Add"));

    const notice = await screen.findByTestId("asset-ref-audience");
    expect(notice.textContent).toContain(NOTICE);
    expect(notice.textContent).toContain("public link");
    // And the one thing adding does not do: the URI still has to be written
    // into the markup, which is what the row's copy control is for.
    expect(notice.textContent).toContain("does not change this asset's content");
  });

  it("offers no file the asset already references", async () => {
    renderPanel();
    fireEvent.click(await screen.findByText("Add"));

    expect(await screen.findByText("Revenue chart")).toBeTruthy();
    // The logo is listed as a reference above; the picker must not offer it
    // again, which is what the server refuses as a conflict.
    const picker = screen.getByTestId("asset-ref-picker");
    expect(picker.textContent).not.toContain("Company logo");
  });

  it("declares the chosen file without touching the asset's content", async () => {
    stubApi(EMPTY);
    renderPanel();

    fireEvent.click(await screen.findByText("Add"));
    fireEvent.click(await screen.findByText("Revenue chart"));

    await waitFor(() => {
      const post = calls.find((c) => c.method === "POST");
      expect(post).toBeTruthy();
      expect(post?.url).toContain(`/assets/${ASSET_ID}/references`);
      expect(post?.body).toContain("res-chart");
      expect(post?.body).toContain('"target_kind":"resource"');
    });
    // Nothing writes the asset's content: the panel declares a reference and
    // hands the author the URI to paste.
    expect(calls.some((c) => c.url.includes("/content"))).toBe(false);
  });

  it("says so instead of offering a picker once the cap is reached", async () => {
    const refs = clone(WITH_LOGO);
    refs.max = 1;
    stubApi(refs);
    renderPanel();

    fireEvent.click(await screen.findByText("Add"));
    expect(await screen.findByText(/maximum number of things/)).toBeTruthy();
    expect(screen.queryByLabelText("Search resources")).toBeNull();
  });
});

describe("removing a reference", () => {
  it("warns with the lines the content still names, and proceeds on confirmation", async () => {
    renderPanel();

    fireEvent.click(await screen.findByLabelText("Remove Company logo"));

    const warning = await screen.findByTestId("asset-ref-remove-warning");
    expect(warning.textContent).toContain("line 2");
    expect(warning.textContent).toContain(LOGO_URI);
    expect(calls.some((c) => c.method === "DELETE")).toBe(false);

    fireEvent.click(screen.getByText("Remove anyway"));
    await waitFor(() => {
      const del = calls.find((c) => c.method === "DELETE");
      expect(del?.url).toContain(`/assets/${ASSET_ID}/references/resource/res-logo`);
    });
  });

  it("cancels without removing", async () => {
    renderPanel();

    fireEvent.click(await screen.findByLabelText("Remove Company logo"));
    fireEvent.click(await screen.findByText("Cancel"));

    await waitFor(() =>
      expect(screen.queryByTestId("asset-ref-remove-warning")).toBeNull(),
    );
    expect(calls.some((c) => c.method === "DELETE")).toBe(false);
  });

  it("removes without a warning when the content does not name the URI", async () => {
    const refs = clone(WITH_LOGO);
    refs.data[0]!.occurrences = [];
    stubApi(refs);
    renderPanel();

    fireEvent.click(await screen.findByLabelText("Remove Company logo"));

    await waitFor(() => expect(calls.some((c) => c.method === "DELETE")).toBe(true));
    expect(screen.queryByTestId("asset-ref-remove-warning")).toBeNull();
  });
});

describe("removing a reference the content could not be checked for", () => {
  // The whole point of the distinction: an unread content reports no
  // occurrences, exactly as a document that names nothing does, and treating
  // the two the same would withdraw a grant from a live report in one click.
  it("confirms rather than removing straight away", async () => {
    const refs = clone(WITH_LOGO);
    refs.content_scanned = false;
    refs.data[0]!.occurrences = [];
    stubApi(refs);
    renderPanel();

    fireEvent.click(await screen.findByLabelText("Remove Company logo"));

    expect(await screen.findByTestId("asset-ref-unchecked-warning")).toBeTruthy();
    expect(calls.some((c) => c.method === "DELETE")).toBe(false);

    fireEvent.click(screen.getByText("Remove anyway"));
    await waitFor(() => expect(calls.some((c) => c.method === "DELETE")).toBe(true));
  });

  it("does not claim a line count it does not have", async () => {
    const refs = clone(WITH_LOGO);
    refs.content_scanned = false;
    refs.data[0]!.occurrences = [];
    stubApi(refs);
    renderPanel();

    fireEvent.click(await screen.findByLabelText("Remove Company logo"));
    const warning = await screen.findByTestId("asset-ref-remove-warning");
    expect(warning.textContent).toContain("could not be checked");
    expect(warning.textContent).not.toMatch(/on 0 lines/);
  });
});

describe("the picker's one page", () => {
  it("says when the library is larger than what it shows", async () => {
    stubApi(EMPTY, { total: 240 });
    renderPanel();

    fireEvent.click(await screen.findByText("Add"));
    const note = await screen.findByTestId("asset-ref-picker-cut");
    expect(note.textContent).toContain("240");
  });

  it("says nothing about a cut when the library fits", async () => {
    stubApi(EMPTY);
    renderPanel();

    fireEvent.click(await screen.findByText("Add"));
    expect(await screen.findByText("Revenue chart")).toBeTruthy();
    expect(screen.queryByTestId("asset-ref-picker-cut")).toBeNull();
  });

  it("asks for a page rather than taking the server default", async () => {
    stubApi(EMPTY);
    renderPanel();

    fireEvent.click(await screen.findByText("Add"));
    await waitFor(() =>
      expect(calls.some((c) => c.url.includes("/api/v1/resources") && c.url.includes("limit="))).toBe(true),
    );
  });
});

// An asset referencing another asset (#1488). The panel is one list over two
// kinds, so what has to hold is that a row says which kind it is, that the
// picker can reach both, and that every write carries the kind -- a resource id
// and an asset id are indistinguishable strings, and a write that dropped the
// kind would address the wrong reference.
const WITH_ASSET: AssetRefsResponse = {
  data: [
    {
      target_kind: "asset",
      target_id: "asset-data",
      uri: "mcp:asset:asset-data",
      position: 0,
      declared_by: "alex.rivera@example.com",
      display_name: "Weekly numbers",
      mime_type: "text/csv",
      size_bytes: 2048,
      owner_email: "sam.chen@example.com",
      content_url: "/portal/refs/asset-q4/tok-data",
      readable: true,
      occurrences: [{ line: 31, snippet: 'fetch("mcp:asset:asset-data")' }],
    },
  ],
  total: 1,
  audience: { public: true, shared_with_users: false },
  can_edit: true,
  max: 20,
  notice: NOTICE,
  content_scanned: true,
};

describe("an asset that references another asset", () => {
  it("names the asset, marks the row as an asset, and shows whose it is", async () => {
    stubApi(WITH_ASSET);
    renderPanel();

    expect(await screen.findByText("Weekly numbers")).toBeTruthy();
    expect(screen.getByTestId("asset-ref-kind-asset")).toBeTruthy();
    expect(screen.getByText("sam.chen@example.com")).toBeTruthy();
    expect(screen.getByText("mcp:asset:asset-data")).toBeTruthy();
    // A CSV is not a picture, so no thumbnail is attempted for it.
    expect(screen.queryByTestId("asset-ref-thumb")).toBeNull();
  });

  it("opens the referenced asset where this reader can read it", async () => {
    stubApi(WITH_ASSET);
    const onNavigate = vi.fn();
    renderPanel({ assetPath: (id) => `/assets/${id}`, onNavigate });

    fireEvent.click(await screen.findByText("Weekly numbers"));
    expect(onNavigate).toHaveBeenCalledWith("/assets/asset-data");
  });

  it("removes it by kind and id, so a resource sharing that id is untouched", async () => {
    stubApi(WITH_ASSET);
    renderPanel();

    fireEvent.click(await screen.findByLabelText("Remove Weekly numbers"));
    fireEvent.click(await screen.findByText("Remove anyway"));

    await waitFor(() => {
      const del = calls.find((c) => c.method === "DELETE");
      expect(del?.url).toContain(`/assets/${ASSET_ID}/references/asset/asset-data`);
    });
  });

  it("flags a reference whose asset was deleted", async () => {
    const refs = clone(WITH_ASSET);
    refs.data[0] = {
      target_kind: "asset",
      target_id: "asset-data",
      uri: "mcp:asset:asset-data",
      position: 0,
      broken: true,
    };
    stubApi(refs);
    renderPanel();

    const broken = await screen.findByTestId("asset-ref-broken");
    expect(broken.textContent).toContain("Missing asset");
  });
});

describe("choosing what to reference", () => {
  it("offers assets on their own tab and declares the chosen one as an asset", async () => {
    stubApi(EMPTY);
    renderPanel();

    fireEvent.click(await screen.findByText("Add"));
    // Radix tabs act on pointer-down, not click.
    fireEvent.mouseDown(screen.getByRole("tab", { name: "Assets" }));

    fireEvent.click(await screen.findByText("Weekly numbers"));

    await waitFor(() => {
      const post = calls.find((c) => c.method === "POST");
      expect(post?.url).toContain(`/assets/${ASSET_ID}/references`);
      expect(post?.body).toContain('"target_kind":"asset"');
      expect(post?.body).toContain("asset-data");
    });
  });

  it("never offers the asset being edited to itself", async () => {
    stubApi(EMPTY);
    renderPanel({ assetId: "asset-data" });

    fireEvent.click(await screen.findByText("Add"));
    fireEvent.mouseDown(screen.getByRole("tab", { name: "Assets" }));

    await waitFor(() =>
      expect(calls.some((c) => c.url.includes("/api/v1/portal/assets?"))).toBe(true),
    );
    expect(screen.queryByText("Weekly numbers")).toBeNull();
  });
});

describe("what the picker's Assets tab can reach", () => {
  // The server admits a reference to any asset the caller may OPEN, shares
  // included, while the asset list route is owner-scoped. A picker built on
  // that list alone would tell someone to ask for a share and then never offer
  // the asset once they had one.
  it("offers an asset somebody shared with this reader", async () => {
    stubApi(EMPTY);
    renderPanel();

    fireEvent.click(await screen.findByText("Add"));
    fireEvent.mouseDown(screen.getByRole("tab", { name: "Assets" }));

    expect(await screen.findByText("Weekly numbers")).toBeTruthy();
    expect(await screen.findByText("Regional rollup")).toBeTruthy();
  });

  // The ranked search answers with scored wrappers rather than assets. A row
  // built from the wrapper renders with no name and picks an undefined id, so
  // the shape is unwrapped where it is read.
  // An asset can be in both lists at once -- a share addressed to its own
  // owner is enough -- and offering it twice is two rows that add the same
  // reference, plus a duplicate-key warning behind them.
  it("offers an asset in both lists exactly once", async () => {
    stubApi(EMPTY, { bothLists: true });
    renderPanel();

    fireEvent.click(await screen.findByText("Add"));
    fireEvent.mouseDown(screen.getByRole("tab", { name: "Assets" }));

    await screen.findByText("Weekly numbers");
    expect(screen.getAllByText("Weekly numbers")).toHaveLength(1);
  });

  it("names the assets a search returns rather than rendering empty rows", async () => {
    stubApi(EMPTY);
    renderPanel();

    fireEvent.click(await screen.findByText("Add"));
    fireEvent.mouseDown(screen.getByRole("tab", { name: "Assets" }));
    fireEvent.change(await screen.findByLabelText("Search assets"), {
      target: { value: "rollup" },
    });

    fireEvent.click(await screen.findByText("Regional rollup"));
    await waitFor(() => {
      const post = calls.find((c) => c.method === "POST");
      expect(post?.body).toContain("asset-shared");
      expect(post?.body).toContain('"target_kind":"asset"');
    });
  });
});
