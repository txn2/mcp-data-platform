import { http, HttpResponse } from "msw";
import type { Asset, AssetMarkEvent, AssetStatusValue } from "@/api/portal/types";
import { contentAssetLinks } from "@/lib/assetLinks";

/**
 * An asset's status (#2502), Verified badge (#2503) and links (#2432), over the
 * mock library. The routes mirror internal/portal/assetstatus, assetverify and
 * assetlinks: a status change is the owner's or an editor's (a viewer gets
 * 403), every change returns the asset as it now reads, and Linked from lists
 * only assets the reader can open.
 */

const PORTAL_BASE = "/api/v1/portal";
const STATUSES = new Set<AssetStatusValue>(["draft", "published", "deprecated"]);

/** The mock reader: the signed-in user /me reports. */
const MOCK_READER = "sarah.chen@example.com";

export interface AssetMarkState {
  /** The library, mutated in place so every other route reads the marks. */
  assets: Asset[];
  /** Each asset's content, as the content route serves it. */
  content: Record<string, string>;
  /** The reader's share permission on assets they do not own. */
  permissions: Map<string, string>;
}

/** Every recorded change, per asset, newest first. */
const history = new Map<string, AssetMarkEvent[]>();

function record(id: string, e: Omit<AssetMarkEvent, "at" | "actor">) {
  const list = history.get(id) ?? [];
  list.unshift({ ...e, actor: MOCK_READER, at: new Date().toISOString() });
  history.set(id, list);
}

/** The asset with its derived marker, as a single read serves it. */
function readRow(a: Asset): Asset {
  if (!a.verification) return a;
  return { ...a, verification: { ...a.verification, changed_since_verified: a.verification.verified_version < a.current_version } };
}

export function assetMarkHandlers(state: AssetMarkState) {
  const find = (id: string) => state.assets.find((a) => a.id === id && !a.deleted_at);
  const canEdit = (id: string) => {
    const perm = state.permissions.get(id);
    return perm === undefined || perm === "editor";
  };
  const notFound = () => HttpResponse.json({ detail: "asset not found" }, { status: 404 });

  return [
    http.put(`${PORTAL_BASE}/assets/:id/status`, async ({ params, request }) => {
      const asset = find(params.id as string);
      if (!asset) return notFound();
      if (!canEdit(asset.id)) {
        return HttpResponse.json({ detail: "only the owner or an editor can change this asset's status" }, { status: 403 });
      }
      const body = (await request.json()) as { status?: string; note?: string };
      const status = (body.status ?? "").toLowerCase() as AssetStatusValue;
      if (!STATUSES.has(status)) {
        return HttpResponse.json({ detail: "invalid status: status must be draft, published or deprecated" }, { status: 400 });
      }
      record(asset.id, { kind: "status", from: asset.status?.value, to: status, note: body.note });
      asset.status = { value: status, note: body.note || undefined, set_by: MOCK_READER, set_at: new Date().toISOString() };
      return HttpResponse.json(readRow(asset));
    }),

    http.delete(`${PORTAL_BASE}/assets/:id/status`, ({ params }) => {
      const asset = find(params.id as string);
      if (!asset) return notFound();
      if (!canEdit(asset.id)) {
        return HttpResponse.json({ detail: "only the owner or an editor can change this asset's status" }, { status: 403 });
      }
      if (asset.status) record(asset.id, { kind: "status", from: asset.status.value });
      delete asset.status;
      return HttpResponse.json(readRow(asset));
    }),

    http.get(`${PORTAL_BASE}/assets/:id/status/history`, ({ params }) => {
      const asset = find(params.id as string);
      if (!asset) return notFound();
      return HttpResponse.json({ events: history.get(asset.id) ?? [] });
    }),

    http.put(`${PORTAL_BASE}/assets/:id/verification`, async ({ params, request }) => {
      const asset = find(params.id as string);
      if (!asset) return notFound();
      const body = (await request.json()) as { note?: string; expires_at?: string };
      if (body.expires_at && new Date(body.expires_at).getTime() <= Date.now()) {
        return HttpResponse.json({ detail: "invalid verification: the expiry must be in the future" }, { status: 400 });
      }
      record(asset.id, { kind: "verification", to: "verified", note: body.note });
      asset.verification = {
        verified_by: MOCK_READER,
        verified_at: new Date().toISOString(),
        verified_version: asset.current_version,
        note: body.note || undefined,
        expires_at: body.expires_at || undefined,
        changed_since_verified: false,
      };
      return HttpResponse.json(readRow(asset));
    }),

    http.delete(`${PORTAL_BASE}/assets/:id/verification`, ({ params }) => {
      const asset = find(params.id as string);
      if (!asset) return notFound();
      if (asset.verification) record(asset.id, { kind: "verification", from: "verified" });
      delete asset.verification;
      return HttpResponse.json(readRow(asset));
    }),

    http.get(`${PORTAL_BASE}/assets/:id/links`, ({ params }) => {
      const asset = find(params.id as string);
      if (!asset) return notFound();
      const links = contentAssetLinks(state.content[asset.id] ?? "").sort();
      // Every live asset in the mock library is one the mock reader can open.
      const linkedFrom = state.assets
        .filter((a) => !a.deleted_at && a.id !== asset.id && contentAssetLinks(state.content[a.id] ?? "").includes(asset.id))
        .map((a) => ({ id: a.id, name: a.name, content_type: a.content_type }))
        .sort((x, y) => x.name.localeCompare(y.name));
      return HttpResponse.json({ links, linked_from: linkedFrom });
    }),
  ];
}

/** The status and Verified filters of a listing, applied to the mock library. */
export function filterByMarks<T extends Pick<Asset, "status" | "verification">>(rows: T[], url: URL): T[] {
  const status = url.searchParams.get("status");
  const verified = url.searchParams.get("verified") === "true";
  return rows.filter((a) => {
    if (status === "none" && a.status) return false;
    if (status && status !== "none" && a.status?.value !== status) return false;
    if (verified && !a.verification) return false;
    return true;
  });
}

/**
 * An asset the mock reader cannot open, owned by someone else (#2432): a link
 * to it lands on the request-access page, as a link to any asset the reader
 * cannot open does. Its single read answers not found like every unknown id;
 * only its access-request page is answered, before the generic one.
 */
export const LOCKED_ASSET_ID = "ast-locked";

export const lockedAssetHandlers = [
  // Any other id returns nothing, which hands the request to the generic
  // access-request mock.
  http.get(`${PORTAL_BASE}/assets/:id/access-request`, ({ params }) =>
    params.id === LOCKED_ASSET_ID ? HttpResponse.json({ has_access: false, owner_email: "sam@example.com" }) : undefined,
  ),
];
