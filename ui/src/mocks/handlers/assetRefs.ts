import { http, HttpResponse } from "msw";
import { mockAssets } from "../data/assets";
import { mockResources } from "../data/resources";
import {
  refKey,
  refsByAsset,
  resolveRefContent,
  uriOf,
  type MockRef,
  type TargetKind,
} from "../data/assetRefs";

export { rewriteRefs } from "../data/assetRefs";

const PORTAL_BASE = "/api/v1/portal";

// The things an asset's content references (#1475, #1488), from both ends.
//
// The fixture is deliberately one asset with a few references rather than every
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

/** view renders one reference the way the portal route does. */
function view(assetId: string, ref: MockRef) {
  const uri = uriOf(ref.kind, ref.target_id);
  const base = {
    target_kind: ref.kind,
    target_id: ref.target_id,
    uri,
    position: 0,
    declared_by: "alice@example.com",
    content_url: `/portal/refs/${assetId}/${ref.token}`,
    readable: true,
    occurrences: ref.lines.map((line) => ({
      line,
      snippet:
        ref.kind === "asset"
          ? `fetch("${uri}").then((r) => r.text())`
          : `<img src="${uri}" alt="referenced file">`,
    })),
  };

  if (ref.kind === "asset") {
    const asset = mockAssets.find((a) => a.id === ref.target_id);
    if (!asset) return { ...base, broken: true, readable: false, occurrences: [] };
    return {
      ...base,
      display_name: asset.name,
      description: asset.description,
      mime_type: asset.content_type,
      size_bytes: asset.size_bytes,
      owner_email: asset.owner_email,
    };
  }

  const res = mockResources.resources.find((r) => r.id === ref.target_id);
  if (!res) return { ...base, broken: true, readable: false, occurrences: [] };
  return {
    ...base,
    display_name: res.display_name,
    filename: res.filename,
    description: res.description,
    path: res.path,
    mime_type: res.mime_type,
    size_bytes: res.size_bytes,
    scope: res.scope,
    scope_id: res.scope_id,
  };
}

function listBody(assetId: string) {
  const refs = refsByAsset[assetId] ?? [];
  return HttpResponse.json({
    data: refs.map((ref) => view(assetId, ref)),
    total: refs.length,
    // The Q4 dashboard carries a public link in the share fixtures, which is
    // the audience the picker names before anything is added to it.
    audience: { public: assetId === "ast-001", shared_with_users: true },
    can_edit: true,
    max: MAX_REFS,
    notice: GRANT_NOTICE,
    content_scanned: true,
  });
}

/**
 * usedBy answers what is holding one target up, for either kind.
 *
 * A reference from an asset the library does not hold is skipped: the capture
 * fixtures are added only for the specs that capture them (#1497), and a row
 * naming an asset nobody can open is not a thing the real route can return.
 */
function usedBy(kind: TargetKind, targetID: string) {
  const data = Object.entries(refsByAsset)
    .filter(([, refs]) => refs.some((r) => r.kind === kind && r.target_id === targetID))
    .flatMap(([assetId]) => {
      const asset = mockAssets.find((a) => a.id === assetId);
      if (!asset) return [];
      return [
        {
          id: assetId,
          name: asset.name,
          owner_email: asset.owner_email,
          public: assetId === "ast-001",
        },
      ];
    });
  return HttpResponse.json({ data, total: data.length, hidden: 0, truncated: false });
}

export const assetRefHandlers = [
  http.get(`${PORTAL_BASE}/assets/:id/references`, ({ params }) =>
    listBody(String(params.id)),
  ),

  http.post(`${PORTAL_BASE}/assets/:id/references`, async ({ params, request }) => {
    const assetId = String(params.id);
    const body = (await request.json()) as { target_kind?: TargetKind; target_id?: string };
    const kind = body.target_kind ?? "resource";
    const targetID = body.target_id ?? "";
    const refs = refsByAsset[assetId] ?? [];
    if (!targetID || refs.some((r) => refKey(r.kind, r.target_id) === refKey(kind, targetID))) {
      return HttpResponse.json(
        { detail: "this asset already references that" },
        { status: 409 },
      );
    }
    // Something added through the panel names no line in the content yet: the
    // author has still to paste the reference into their markup, which is why
    // the row carries a copy control.
    refsByAsset[assetId] = [
      ...refs,
      { kind, target_id: targetID, token: `ref-tok-${targetID}`, lines: [] },
    ];
    return listBody(assetId);
  }),

  http.delete(`${PORTAL_BASE}/assets/:id/references/:kind/:targetID`, ({ params }) => {
    const assetId = String(params.id);
    const key = refKey(params.kind as TargetKind, String(params.targetID));
    refsByAsset[assetId] = (refsByAsset[assetId] ?? []).filter(
      (r) => refKey(r.kind, r.target_id) !== key,
    );
    return listBody(assetId);
  }),

  http.get(`${PORTAL_BASE}/resources/:id/used-by`, ({ params }) =>
    usedBy("resource", String(params.id)),
  ),

  http.get(`${PORTAL_BASE}/assets/:id/used-by`, ({ params }) =>
    usedBy("asset", String(params.id)),
  ),

  // The reference-serving route. It is not under /api/v1: the real one takes no
  // session, because the content that carries these URLs renders inside a
  // sandboxed iframe and on public shares. The panel's thumbnails load through
  // it, so a mock without it would show a column of broken images.
  http.get("/portal/refs/:assetId/:token", ({ params }) => {
    const content = resolveRefContent(String(params.token));
    if (!content) {
      return HttpResponse.json({ detail: "no such reference" }, { status: 404 });
    }
    if (typeof content.body === "string") {
      return HttpResponse.text(content.body, { headers: { "Content-Type": content.contentType } });
    }
    return HttpResponse.arrayBuffer(content.body.buffer as ArrayBuffer, {
      headers: { "Content-Type": content.contentType },
    });
  }),
];
