import { mockAssets } from "./assets";
import { mockResources } from "./resources";
import { resourceImageBytes } from "./resourceImages";

/**
 * The reference fixture: what each asset's content names, the token each
 * reference is served under, and the bytes behind it.
 *
 * It lives beside the other fixture data rather than inside the MSW handlers
 * because two servers answer the reference route in the mocked portal. The
 * service worker answers the page's own requests; it does not control a
 * sandboxed blob: frame, which is exactly where a referencing artifact renders
 * and where a thumbnail is captured (#1497), so the dev server answers those
 * from this same table (see mockRefRoute in vite.config.ts). Two tables would
 * mean an artifact that loads its references in one surface and not the other.
 */

export type TargetKind = "resource" | "asset";

export interface MockRef {
  kind: TargetKind;
  target_id: string;
  token: string;
  /** The lines the asset's stored content writes this reference on. */
  lines: number[];
}

// refsByAsset is mutable: adding and removing through the panel has to be
// visible in the demo, and a fixture that answered the same list after a write
// would make the panel look broken.
//
// ast-001 carries both kinds, which is the state #1488 introduces: a dashboard
// showing a logo and reading a data asset another job refreshes.
export const refsByAsset: Record<string, MockRef[]> = {
  "ast-001": [
    { kind: "resource", target_id: "res-029", token: "ref-tok-029", lines: [14] },
    { kind: "resource", target_id: "res-031", token: "ref-tok-031", lines: [22, 48] },
    { kind: "asset", target_id: "ast-004", token: "ref-tok-ast-004", lines: [31] },
  ],
  // The two artifacts the thumbnail suite captures (#1497): one whose
  // references resolve and one whose reference is a target that is gone. The
  // second is what proves a capture of an error branch is thrown away rather
  // than stored, so its target deliberately has no bytes behind it.
  "ast-011": [
    { kind: "resource", target_id: "res-029", token: "ref-tok-011-img", lines: [6] },
    { kind: "asset", target_id: "ast-008", token: "ref-tok-011-data", lines: [10] },
  ],
  "ast-012": [
    { kind: "resource", target_id: "res-gone", token: "ref-tok-012-img", lines: [6] },
  ],
};

/** refKey identifies a reference within one asset, kind included. */
export function refKey(kind: TargetKind, id: string): string {
  return `${kind}:${id}`;
}

/** uriOf is the address a target is referenced by, in its kind's form. */
export function uriOf(kind: TargetKind, targetID: string): string {
  if (kind === "asset") {
    return `mcp:asset:${targetID}`;
  }
  const res = mockResources.resources.find((r) => r.id === targetID);
  if (!res) return `mcp://global/deleted/${targetID}`;
  return res.scope === "global"
    ? `mcp://global/${res.path}/${res.filename}`
    : `mcp://${res.scope}/${res.scope_id}/${res.path}/${res.filename}`;
}

/**
 * rewriteRefs is the mock's copy of what every viewing surface does on the way
 * out: a declared mcp:// URI or mcp:asset:<id> reference in the content becomes
 * the URL its reference is served under (internal/portal/assetrefs.Rewrite).
 *
 * The URL is absolute for the reason assetrefs.URL is absolute: the content
 * renders inside an iframe whose document came from a blob: URL, and a
 * root-relative path resolved against a blob: base does not name the server at
 * all. Without the rewrite the routes serve the URIs as stored, and an artifact
 * in the mocked portal shows a broken image where a referenced logo belongs.
 */
export function rewriteRefs(assetId: string, body: string, base?: string): string {
  const origin = base ?? (typeof location === "undefined" ? "" : location.origin);
  let out = body;
  for (const ref of refsByAsset[assetId] ?? []) {
    out = out
      .split(uriOf(ref.kind, ref.target_id))
      .join(`${origin}/portal/refs/${assetId}/${ref.token}`);
  }
  return out;
}

/** One reference's bytes and the type they are served as. */
export interface RefContent {
  body: Uint8Array | string;
  contentType: string;
}

/**
 * knowsRefToken reports whether this fixture table holds the token at all.
 *
 * It is separate from resolving the bytes because the two answers are acted on
 * differently by the dev server: a token the fixtures do not hold is a real
 * backend's, and is left alone, while a token they hold whose target is gone is
 * the 404 the real route answers (and the failure a capture is discarded over).
 */
export function knowsRefToken(token: string): boolean {
  return Object.values(refsByAsset)
    .flat()
    .some((r) => r.token === token);
}

/**
 * resolveRefContent answers the reference route: the bytes behind one token, or
 * null where the token names nothing or its target is gone -- which is the 404
 * the real route answers and the failure a capture is discarded over.
 */
export function resolveRefContent(token: string): RefContent | null {
  const ref = Object.values(refsByAsset)
    .flat()
    .find((r) => r.token === token);
  if (!ref) return null;
  if (ref.kind === "asset") {
    const asset = mockAssets.find((a) => a.id === ref.target_id);
    if (!asset) return null;
    return { body: `region,revenue\nwest,${asset.size_bytes}\n`, contentType: "text/csv" };
  }
  const image = resourceImageBytes(ref.target_id);
  if (!image) return null;
  return { body: image, contentType: "image/png" };
}
