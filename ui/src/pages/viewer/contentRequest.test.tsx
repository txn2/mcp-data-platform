import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";

import { AssetViewerPage } from "./AssetViewerPage";
import { AdminAssetViewerPage } from "./AdminAssetViewerPage";
import { AssetPreviewModal } from "@/components/AssetPreviewModal";
import { TEXT_INLINE_LIMIT, VIRTUALIZED_INLINE_LIMIT } from "@/components/renderers/registry";

// #1883: a CSV written by a managed script, with two versions and a name with
// no extension, sat on "Loading..." with no content request made. The page it
// was seen on ran a release without #1874, whose flat 2 MB fetch gate is what
// left a 3 MB CSV neither read nor refused. These pin the case through the real
// pages rather than through the rule alone: every inline family, just under its
// limit, reaches exactly one content request whatever the asset's version
// count, owner kind and name, on the asset page, the admin asset viewer and the
// preview modal.

const ID = "a1";

/** One inline family: its type, a name with its extension, its limit and a body. */
const FAMILIES = [
  { type: "text/csv", ext: ".csv", limit: VIRTUALIZED_INLINE_LIMIT, body: "region,total\nwest,4\n" },
  { type: "text/tab-separated-values", ext: ".tsv", limit: VIRTUALIZED_INLINE_LIMIT, body: "region\ttotal\nwest\t4\n" },
  { type: "application/json", ext: ".json", limit: VIRTUALIZED_INLINE_LIMIT, body: '{"region":"west"}' },
  { type: "application/x-ndjson", ext: ".jsonl", limit: VIRTUALIZED_INLINE_LIMIT, body: '{"region":"west"}\n' },
  { type: "text/markdown", ext: ".md", limit: TEXT_INLINE_LIMIT, body: "# west\n" },
  { type: "text/plain", ext: ".txt", limit: TEXT_INLINE_LIMIT, body: "west\n" },
  { type: "application/xml", ext: ".xml", limit: TEXT_INLINE_LIMIT, body: "<r>west</r>\n" },
  { type: "application/yaml", ext: ".yaml", limit: TEXT_INLINE_LIMIT, body: "region: west\n" },
  { type: "text/html", ext: ".html", limit: TEXT_INLINE_LIMIT, body: "<p>west</p>" },
];

const OWNERS = [
  { owner_id: "user-1", owner_email: "owner@example.com" },
  // A script-written asset is owned by the script's principal.
  { owner_id: "script:daily-export", owner_email: "" },
];

interface Shape {
  type: string;
  size: number;
  name: string;
  versions: number;
  owner: (typeof OWNERS)[number];
  body: string;
}

function record(s: Shape) {
  return {
    id: ID,
    ...s.owner,
    name: s.name,
    description: "",
    content_type: s.type,
    s3_bucket: "b",
    s3_key: "k",
    size_bytes: s.size,
    tags: [],
    provenance: {},
    current_version: s.versions,
    is_owner: true,
    created_at: "2026-09-01T00:00:00Z",
    updated_at: "2026-09-01T00:00:00Z",
  };
}

function versions(s: Shape) {
  const data = Array.from({ length: s.versions }, (_, i) => ({
    id: `v${i + 1}`,
    asset_id: ID,
    version: s.versions - i,
    s3_key: "k",
    s3_bucket: "b",
    content_type: s.type,
    size_bytes: s.size,
    created_by: s.owner.owner_id,
    change_summary: "",
    created_at: "2026-09-01T00:00:00Z",
  }));
  return { data, total: data.length };
}

/**
 * Answers the routes the pages read. The content route answers with the body;
 * the record and its versions with the shape; every list the page shows beside
 * them with an empty one.
 */
function stubServer(s: Shape) {
  const json = (v: unknown) =>
    Promise.resolve(new Response(JSON.stringify(v), { status: 200, headers: { "Content-Type": "application/json" } }));
  const fetchMock = vi.fn((input: RequestInfo | URL) => {
    const url = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
    const path = url.split("?")[0] ?? "";
    if (path.endsWith(`/assets/${ID}/content`)) {
      return Promise.resolve(new Response(s.body, { status: 200, headers: { "Content-Type": s.type } }));
    }
    if (path.endsWith(`/assets/${ID}`)) return json(record(s));
    if (path.endsWith(`/assets/${ID}/versions`)) return json(versions(s));
    if (path.endsWith(`/assets/${ID}/shares`)) return json([]);
    return json({ data: [], total: 0, pages: [], items: [] });
  });
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

function contentRequests(fetchMock: ReturnType<typeof stubServer>): string[] {
  return fetchMock.mock.calls
    .map(([input]) => (typeof input === "string" ? input : input instanceof URL ? input.href : input.url))
    .filter((u) => u.split("?")[0]?.endsWith(`/assets/${ID}/content`));
}

function withQuery(node: ReactNode) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return <QueryClientProvider client={qc}>{node}</QueryClientProvider>;
}

/** Every shape the matrix asks about: family x version count x owner x name. */
function shapes(): Array<{ label: string; shape: Shape }> {
  const out: Array<{ label: string; shape: Shape }> = [];
  for (const f of FAMILIES) {
    for (const v of [1, 3]) {
      for (const owner of OWNERS) {
        for (const named of [true, false]) {
          const name = named ? `daily${f.ext}` : "daily";
          out.push({
            label: `${f.type}, ${v} version(s), ${owner.owner_id}, "${name}"`,
            shape: { type: f.type, size: f.limit - 1, name, versions: v, owner, body: f.body },
          });
        }
      }
    }
  }
  return out;
}

const SURFACES: Array<{ name: string; route: string; mount: (s: Shape) => ReactNode }> = [
  {
    name: "asset page",
    route: `/api/v1/portal/assets/${ID}/content`,
    mount: () => <AssetViewerPage assetId={ID} onNavigate={() => {}} onBack={() => {}} />,
  },
  {
    name: "admin asset viewer",
    route: `/api/v1/admin/assets/${ID}/content`,
    mount: () => <AdminAssetViewerPage assetId={ID} onNavigate={() => {}} />,
  },
  {
    name: "preview modal",
    route: `/api/v1/portal/assets/${ID}/content`,
    mount: (s) => (
      <AssetPreviewModal assetId={ID} assetName={s.name} contentType={s.type} sizeBytes={s.size} onClose={() => {}} />
    ),
  },
];

describe("every inline family under its limit reaches one content request (#1883)", () => {
  afterEach(() => vi.unstubAllGlobals());

  for (const surface of SURFACES) {
    it(`on the ${surface.name}, whatever the version count, owner and name`, async () => {
      for (const { label, shape } of shapes()) {
        const fetchMock = stubServer(shape);
        const view = render(withQuery(surface.mount(shape)));
        await waitFor(() => expect(contentRequests(fetchMock), label).toHaveLength(1));
        expect(contentRequests(fetchMock)[0]?.split("?")[0], label).toBe(surface.route);
        view.unmount();
        vi.unstubAllGlobals();
      }
    });
  }

  // The asset #1883 was reported against, as it was stored: 3,140,976 bytes of
  // CSV, two versions, written by a script, named without an extension. It
  // renders in the table viewer rather than sitting on the loading indicator.
  it("renders the reported script-written CSV in the table viewer on all three surfaces", async () => {
    const shape: Shape = {
      type: "text/csv",
      size: 3_140_976,
      name: "daily-export",
      versions: 2,
      owner: OWNERS[1]!,
      body: "region,total\nwest,4\n",
    };
    for (const surface of SURFACES) {
      const fetchMock = stubServer(shape);
      const view = render(withQuery(surface.mount(shape)));
      expect(await screen.findByText("west"), surface.name).toBeInTheDocument();
      await waitFor(() => expect(screen.queryByText("Loading..."), surface.name).not.toBeInTheDocument());
      expect(contentRequests(fetchMock), surface.name).toHaveLength(1);
      view.unmount();
      vi.unstubAllGlobals();
    }
  });
});
