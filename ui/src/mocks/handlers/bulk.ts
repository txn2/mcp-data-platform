import { http, HttpResponse } from "msw";
import type { BulkRequest, BulkResult, TagPage, TagRow } from "@/api/portal/hooks/bulk";
import { REASON_HIDDEN } from "@/api/portal/hooks/bulk";
import { mockAssets } from "../data/assets";

// Bulk actions on assets (#2507) and the tag list (#2508):
//   POST /api/v1/portal/assets/bulk
//   GET  /api/v1/portal/tags
//   GET  /api/v1/admin/tags, POST /api/v1/admin/tags/rename, POST /api/v1/admin/tags/merge

const PORTAL = "/api/v1/portal";
const ADMIN = "/api/v1/admin";

/** The tags on the mock assets, compared without case, most carried first. */
export function tagRows(prefix: string): TagRow[] {
  const groups = new Map<string, Map<string, number>>();
  const lowerPrefix = prefix.trim().toLowerCase();
  for (const a of mockAssets) {
    for (const t of new Set(a.tags ?? [])) {
      const key = t.toLowerCase();
      if (!key.startsWith(lowerPrefix)) continue;
      const spellings = groups.get(key) ?? new Map<string, number>();
      spellings.set(t, (spellings.get(t) ?? 0) + 1);
      groups.set(key, spellings);
    }
  }
  const rows: TagRow[] = [];
  for (const [key, spellings] of groups) {
    const list = [...spellings.entries()]
      .map(([tag, count]) => ({ tag, count }))
      .sort((x, y) => y.count - x.count || x.tag.localeCompare(y.tag));
    const count = mockAssets.filter((a) => (a.tags ?? []).some((t) => t.toLowerCase() === key)).length;
    rows.push({ tag: list[0]!.tag, count, spellings: list });
  }
  return rows.sort((x, y) => y.count - x.count || x.tag.localeCompare(y.tag));
}

function page(prefix: string, limit: number): TagPage {
  const rows = tagRows(prefix);
  return { tags: rows.slice(0, limit), total: rows.length };
}

/** applyOne mirrors the server for the mock: unknown ids are hidden, the rest change. */
function applyOne(req: BulkRequest, id: string): string {
  const asset = mockAssets.find((a) => a.id === id);
  if (!asset) return REASON_HIDDEN;
  const tags = req.tags ?? [];
  if (req.action === "add_tags") {
    const has = new Set((asset.tags ?? []).map((t) => t.toLowerCase()));
    asset.tags = [...(asset.tags ?? []), ...tags.filter((t) => !has.has(t.toLowerCase()))];
  } else if (req.action === "remove_tags") {
    const drop = new Set(tags.map((t) => t.toLowerCase()));
    asset.tags = (asset.tags ?? []).filter((t) => !drop.has(t.toLowerCase()));
  }
  return "";
}

function retag(sources: string[], target: string) {
  const match = new Set([...sources, target].map((t) => t.toLowerCase()));
  let moved = 0;
  for (const a of mockAssets) {
    const tags = a.tags ?? [];
    if (!tags.some((t) => match.has(t.toLowerCase()) && t !== target)) continue;
    a.tags = [...new Set(tags.map((t) => (match.has(t.toLowerCase()) ? target : t)))];
    moved++;
  }
  return { tag: target, moved };
}

export const bulkHandlers = [
  http.post(`${PORTAL}/assets/bulk`, async ({ request }) => {
    const req = (await request.json()) as BulkRequest;
    const ids = [...new Set(req.asset_ids ?? [])];
    if (ids.length === 0 || ids.length > 500) {
      return HttpResponse.json({ detail: "asset_ids is required: name at least one asset" }, { status: 400 });
    }
    const result: BulkResult = { action: req.action, requested: ids.length, applied: [], skipped: [] };
    for (const id of ids) {
      const reason = applyOne(req, id);
      if (reason) result.skipped.push({ asset_id: id, reason });
      else result.applied.push(id);
    }
    return HttpResponse.json(result);
  }),

  http.get(`${PORTAL}/tags`, ({ request }) => {
    const url = new URL(request.url);
    return HttpResponse.json(page(url.searchParams.get("prefix") ?? "", Number(url.searchParams.get("limit") ?? 50)));
  }),

  http.get(`${ADMIN}/tags`, ({ request }) => {
    const url = new URL(request.url);
    return HttpResponse.json(page(url.searchParams.get("q") ?? "", Number(url.searchParams.get("limit") ?? 50)));
  }),

  http.post(`${ADMIN}/tags/rename`, async ({ request }) => {
    const body = (await request.json()) as { from: string; to: string };
    return HttpResponse.json(retag([body.from], body.to));
  }),

  http.post(`${ADMIN}/tags/merge`, async ({ request }) => {
    const body = (await request.json()) as { from: string[]; into: string };
    return HttpResponse.json(retag(body.from, body.into));
  }),
];
