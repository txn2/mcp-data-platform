import { http, HttpResponse } from "msw";
import { mockAssets } from "../data/assets";
import { mockResources } from "../data/resources";
import { resourceImageBytes } from "../data/resourceImages";

const PORTAL_BASE = "/api/v1/portal";

// The managed resources an asset's content references (#1475), from both ends.
//
// The fixture is deliberately one asset with two references rather than every
// asset with one: most assets reference nothing, and a library where they all
// do would show the panel in a state a deployment rarely sees. The references
// are on ast-001, the Q4 dashboard, which is the asset the screenshot manifest
// opens.

// GRANT_NOTICE is the server's own sentence, repeated here because the panel
// renders whatever the list sends and a mock that shortened it would show a
// picker that reads differently from the real one.
const GRANT_NOTICE =
  "Anyone this asset is shared with can load these files through it, " +
  "including anyone holding a public link, now and later.";

const MAX_REFS = 20;

interface MockRef {
  resource_id: string;
  token: string;
  /** The lines the asset's stored content writes this URI on. */
  lines: number[];
}

// refsByAsset is mutable: adding and removing through the panel has to be
// visible in the demo, and a fixture that answered the same list after a write
// would make the panel look broken.
const refsByAsset: Record<string, MockRef[]> = {
  "ast-001": [
    { resource_id: "res-029", token: "ref-tok-029", lines: [14] },
    { resource_id: "res-031", token: "ref-tok-031", lines: [22, 48] },
  ],
};

/** uriOf is the mcp:// address a resource is referenced by. */
function uriOf(resourceId: string): string {
  const res = mockResources.resources.find((r) => r.id === resourceId);
  if (!res) return `mcp://global/deleted/${resourceId}`;
  return res.scope === "global"
    ? `mcp://global/${res.category}/${res.filename}`
    : `mcp://${res.scope}/${res.scope_id}/${res.category}/${res.filename}`;
}

/** view renders one reference the way the portal route does. */
function view(assetId: string, ref: MockRef) {
  const res = mockResources.resources.find((r) => r.id === ref.resource_id);
  const uri = uriOf(ref.resource_id);
  if (!res) {
    return { resource_id: ref.resource_id, uri, position: 0, broken: true };
  }
  return {
    resource_id: res.id,
    uri,
    position: 0,
    declared_by: "alice@example.com",
    display_name: res.display_name,
    filename: res.filename,
    description: res.description,
    category: res.category,
    mime_type: res.mime_type,
    size_bytes: res.size_bytes,
    scope: res.scope,
    scope_id: res.scope_id,
    content_url: `/portal/refs/${assetId}/${ref.token}`,
    readable: true,
    occurrences: ref.lines.map((line) => ({
      line,
      snippet: `<img src="${uri}" alt="${res.display_name}">`,
    })),
  };
}

function listBody(assetId: string) {
  const refs = refsByAsset[assetId] ?? [];
  return HttpResponse.json({
    data: refs.map((ref) => view(assetId, ref)),
    total: refs.length,
    // The Q4 dashboard carries a public link in the share fixtures, which is
    // the audience the picker names before a file is added to it.
    audience: { public: assetId === "ast-001", shared_with_users: true },
    can_edit: true,
    max: MAX_REFS,
    notice: GRANT_NOTICE,
    content_scanned: true,
  });
}

export const assetResourceRefHandlers = [
  http.get(`${PORTAL_BASE}/assets/:id/resources`, ({ params }) =>
    listBody(String(params.id)),
  ),

  http.post(`${PORTAL_BASE}/assets/:id/resources`, async ({ params, request }) => {
    const assetId = String(params.id);
    const body = (await request.json()) as { resource_id?: string };
    const resourceId = body.resource_id ?? "";
    const refs = refsByAsset[assetId] ?? [];
    if (!resourceId || refs.some((r) => r.resource_id === resourceId)) {
      return HttpResponse.json(
        { detail: "this asset already references that file" },
        { status: 409 },
      );
    }
    // A file added through the panel names no line in the content yet: the
    // author has still to paste the URI into their markup, which is why the
    // row carries a copy control.
    refsByAsset[assetId] = [...refs, { resource_id: resourceId, token: `ref-tok-${resourceId}`, lines: [] }];
    return listBody(assetId);
  }),

  http.delete(`${PORTAL_BASE}/assets/:id/resources/:resourceID`, ({ params }) => {
    const assetId = String(params.id);
    refsByAsset[assetId] = (refsByAsset[assetId] ?? []).filter(
      (r) => r.resource_id !== String(params.resourceID),
    );
    return listBody(assetId);
  }),

  http.get(`${PORTAL_BASE}/resources/:id/assets`, ({ params }) => {
    const resourceId = String(params.id);
    const data = Object.entries(refsByAsset)
      .filter(([, refs]) => refs.some((r) => r.resource_id === resourceId))
      .map(([assetId]) => {
        const asset = mockAssets.find((a) => a.id === assetId);
        return {
          id: assetId,
          name: asset?.name ?? assetId,
          owner_email: asset?.owner_email,
          public: assetId === "ast-001",
        };
      });
    return HttpResponse.json({ data, total: data.length, hidden: 0, truncated: false });
  }),

  // The reference-serving route. It is not under /api/v1: the real one takes no
  // session, because the content that carries these URLs renders inside a
  // sandboxed iframe and on public shares. The panel's thumbnails load through
  // it, so a mock without it would show a column of broken images.
  http.get("/portal/refs/:assetId/:token", ({ params }) => {
    const ref = Object.values(refsByAsset)
      .flat()
      .find((r) => r.token === String(params.token));
    const image = ref && resourceImageBytes(ref.resource_id);
    if (!image) {
      return HttpResponse.json({ detail: "no such reference" }, { status: 404 });
    }
    return HttpResponse.arrayBuffer(image.buffer as ArrayBuffer, {
      headers: { "Content-Type": "image/png" },
    });
  }),
];
