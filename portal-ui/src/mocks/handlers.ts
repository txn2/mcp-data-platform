import { http, HttpResponse } from "msw";
import type { Share } from "@/api/types";
import { mockAssets, mockShares, mockSharedWithMe } from "./data/assets";
import { mockContent } from "./data/content";

const BASE = "/api/v1/portal";

// Mutable copies so mutations (create, revoke, delete) work within a session.
let assets = [...mockAssets];
const shares: Record<string, Share[]> = JSON.parse(
  JSON.stringify(mockShares),
);

let shareCounter = 100;

export const handlers = [
  // -----------------------------------------------------------------------
  // Branding (public, unauthenticated)
  // -----------------------------------------------------------------------

  http.get("/api/v1/admin/public/branding", () => {
    return HttpResponse.json({
      name: "ACME Data Platform",
      portal_title: "ACME Data Platform",
      portal_logo: "",
      portal_logo_light: "",
      portal_logo_dark: "",
    });
  }),

  // -----------------------------------------------------------------------
  // Assets
  // -----------------------------------------------------------------------

  // GET /assets — paginated list with optional filters
  http.get(`${BASE}/assets`, ({ request }) => {
    const url = new URL(request.url);
    const contentType = url.searchParams.get("content_type");
    const tag = url.searchParams.get("tag");
    const limit = parseInt(url.searchParams.get("limit") ?? "50", 10);
    const offset = parseInt(url.searchParams.get("offset") ?? "0", 10);

    let filtered = assets.filter((a) => !a.deleted_at);

    if (contentType) {
      filtered = filtered.filter((a) => a.content_type === contentType);
    }
    if (tag) {
      filtered = filtered.filter((a) =>
        a.tags.some((t) => t.toLowerCase().includes(tag.toLowerCase())),
      );
    }

    const page = filtered.slice(offset, offset + limit);

    return HttpResponse.json({
      data: page,
      total: filtered.length,
      limit,
      offset,
    });
  }),

  // GET /assets/:id — single asset
  http.get(`${BASE}/assets/:id`, ({ params }) => {
    const asset = assets.find((a) => a.id === params.id && !a.deleted_at);
    if (!asset) {
      return HttpResponse.json({ detail: "Not found" }, { status: 404 });
    }
    return HttpResponse.json(asset);
  }),

  // GET /assets/:id/content — raw content
  http.get(`${BASE}/assets/:id/content`, ({ params }) => {
    const id = params.id as string;
    const asset = assets.find((a) => a.id === id && !a.deleted_at);
    if (!asset) {
      return HttpResponse.json({ detail: "Not found" }, { status: 404 });
    }
    const body = mockContent[id] ?? `[Mock content for ${asset.name}]`;
    return new HttpResponse(body, {
      headers: { "Content-Type": asset.content_type },
    });
  }),

  // PUT /assets/:id — update name/description/tags
  http.put(`${BASE}/assets/:id`, async ({ params, request }) => {
    const idx = assets.findIndex(
      (a) => a.id === params.id && !a.deleted_at,
    );
    if (idx === -1) {
      return HttpResponse.json({ detail: "Not found" }, { status: 404 });
    }
    const body = (await request.json()) as Record<string, unknown>;
    if (body.name !== undefined) assets[idx]!.name = body.name as string;
    if (body.description !== undefined)
      assets[idx]!.description = body.description as string;
    if (body.tags !== undefined)
      assets[idx]!.tags = body.tags as string[];
    assets[idx]!.updated_at = new Date().toISOString();
    return HttpResponse.json(assets[idx]);
  }),

  // DELETE /assets/:id — soft delete
  http.delete(`${BASE}/assets/:id`, ({ params }) => {
    const idx = assets.findIndex(
      (a) => a.id === params.id && !a.deleted_at,
    );
    if (idx === -1) {
      return HttpResponse.json({ detail: "Not found" }, { status: 404 });
    }
    assets[idx]!.deleted_at = new Date().toISOString();
    return new HttpResponse(null, { status: 204 });
  }),

  // -----------------------------------------------------------------------
  // Shares
  // -----------------------------------------------------------------------

  // GET /assets/:id/shares — list shares for an asset
  http.get(`${BASE}/assets/:assetId/shares`, ({ params }) => {
    const assetId = params.assetId as string;
    return HttpResponse.json(shares[assetId] ?? []);
  }),

  // POST /assets/:id/shares — create a share
  http.post(`${BASE}/assets/:assetId/shares`, async ({ params, request }) => {
    const assetId = params.assetId as string;
    const body = (await request.json()) as Record<string, unknown>;

    shareCounter++;
    const token = `tok_mock_${shareCounter}_${Math.random().toString(36).slice(2, 10)}`;

    const share: Share = {
      id: `shr-mock-${shareCounter}`,
      asset_id: assetId,
      token,
      created_by: "user-alice",
      shared_with_user_id: body.shared_with_user_id as string | undefined,
      expires_at: body.expires_in
        ? new Date(
            Date.now() + parseDuration(body.expires_in as string),
          ).toISOString()
        : undefined,
      revoked: false,
      access_count: 0,
      created_at: new Date().toISOString(),
    };

    if (!shares[assetId]) shares[assetId] = [];
    shares[assetId]!.push(share);

    return HttpResponse.json({
      share,
      share_url: `${window.location.origin}/portal/view/${token}`,
    });
  }),

  // DELETE /shares/:id — revoke a share
  http.delete(`${BASE}/shares/:id`, ({ params }) => {
    for (const list of Object.values(shares)) {
      const share = list.find((s) => s.id === params.id);
      if (share) {
        share.revoked = true;
        return new HttpResponse(null, { status: 204 });
      }
    }
    return HttpResponse.json({ detail: "Not found" }, { status: 404 });
  }),

  // -----------------------------------------------------------------------
  // Shared with me
  // -----------------------------------------------------------------------

  http.get(`${BASE}/shared-with-me`, ({ request }) => {
    const url = new URL(request.url);
    const limit = parseInt(url.searchParams.get("limit") ?? "50", 10);
    const offset = parseInt(url.searchParams.get("offset") ?? "0", 10);

    const page = mockSharedWithMe.slice(offset, offset + limit);

    return HttpResponse.json({
      data: page,
      total: mockSharedWithMe.length,
      limit,
      offset,
    });
  }),
];

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function parseDuration(s: string): number {
  const match = s.match(/^(\d+)(h|m|s)$/);
  if (!match) return 24 * 60 * 60 * 1000; // default 24h
  const [, val, unit] = match;
  const n = parseInt(val!, 10);
  switch (unit) {
    case "h":
      return n * 60 * 60 * 1000;
    case "m":
      return n * 60 * 1000;
    case "s":
      return n * 1000;
    default:
      return 24 * 60 * 60 * 1000;
  }
}
