import { http, HttpResponse } from "msw";
import type { DuplicateGroup, LibraryItem, TrashItem, UnusedItem } from "@/api/portal/hooks/library";
import { mockAssets } from "../data/assets";
import { mockCollections } from "../data/collections";

// A person's library (#2504, #2509) for the demo and the screenshots: recents
// and favorites over the mock assets and collections, a trash, unused assets
// and one possible-duplicates group. Stars and dismissals change the lists for
// the life of the page.

const PORTAL_BASE = "/api/v1/portal";
const DAY = 24 * 60 * 60 * 1000;
const ago = (ms: number) => new Date(Date.now() - ms).toISOString();

function item(kind: "asset" | "collection", id: string, at: string): LibraryItem | null {
  if (kind === "collection") {
    const c = mockCollections.find((x) => x.id === id);
    return c ? { kind, id, name: c.name, owner_email: c.owner_email, at } : null;
  }
  const a = mockAssets.find((x) => x.id === id);
  return a ? { kind, id, name: a.name, content_type: a.content_type, owner_email: a.owner_email, at } : null;
}

const recents = (): LibraryItem[] =>
  [
    item("asset", "ast-001", ago(4 * 60 * 1000)),
    item("asset", "ast-002", ago(60 * 60 * 1000)),
    item("collection", "col-001", ago(DAY)),
    item("asset", "ast-003", ago(2 * DAY)),
  ].filter((x): x is LibraryItem => x !== null);

let favorites: LibraryItem[] = [
  item("asset", "ast-001", ago(3 * DAY)),
  item("asset", "ast-004", ago(5 * DAY)),
  item("collection", "col-001", ago(6 * DAY)),
].filter((x): x is LibraryItem => x !== null);

let trash: TrashItem[] = [
  { kind: "asset", id: "ast-trash-1", name: "Old pricing model", content_type: "text/html", owner_email: "alice@example.com", deleted_at: ago(2 * DAY), purge_at: new Date(Date.now() + 28 * DAY).toISOString() },
  { kind: "collection", id: "col-trash-1", name: "Q2 scratch charts", owner_email: "alice@example.com", deleted_at: ago(17 * DAY), purge_at: new Date(Date.now() + 13 * DAY).toISOString() },
  { kind: "asset", id: "ast-trash-2", name: "Support trend draft", content_type: "text/markdown", owner_email: "alice@example.com", deleted_at: ago(28 * DAY), purge_at: new Date(Date.now() + 2 * DAY).toISOString() },
];

const unused: UnusedItem[] = [
  { id: "ast-003", name: mockAssets[2]?.name ?? "Inventory snapshot", content_type: "text/html", owner_email: "alice@example.com", status: "", opens: 3, last_opened: ago(120 * DAY), created_at: ago(140 * DAY) },
  { id: "ast-005", name: mockAssets[4]?.name ?? "Vendor list export", content_type: "text/csv", owner_email: "alice@example.com", status: "draft", opens: 0, created_at: ago(100 * DAY) },
];

const dismissed = new Set<string>();

const groups = (): DuplicateGroup[] =>
  [
    {
      digest: "a".repeat(64),
      similarity: 96,
      members: [
        { id: "ast-001", name: mockAssets[0]?.name ?? "Q3 revenue by region", owner_email: "alice@example.com", version: 4, updated_at: ago(7 * DAY), status: "published", opens: 41 },
        { id: "ast-002", name: mockAssets[1]?.name ?? "Revenue by region Q3 (copy)", owner_email: "alice@example.com", version: 1, updated_at: ago(10 * DAY), opens: 2 },
      ],
    },
  ].filter((g) => !dismissed.has(g.digest));

export const libraryHandlers = [
  http.get(`${PORTAL_BASE}/recents`, () => HttpResponse.json({ items: recents() })),
  http.post(`${PORTAL_BASE}/recents`, () => new HttpResponse(null, { status: 204 })),
  http.get(`${PORTAL_BASE}/favorites`, () => HttpResponse.json({ items: favorites })),
  http.put(`${PORTAL_BASE}/favorites/:kind/:id`, ({ params }) => {
    const entry = item(params.kind as "asset" | "collection", String(params.id), new Date().toISOString());
    if (entry && !favorites.some((f) => f.kind === entry.kind && f.id === entry.id)) favorites = [entry, ...favorites];
    return new HttpResponse(null, { status: 204 });
  }),
  http.delete(`${PORTAL_BASE}/favorites/:kind/:id`, ({ params }) => {
    favorites = favorites.filter((f) => !(f.kind === params.kind && f.id === params.id));
    return new HttpResponse(null, { status: 204 });
  }),
  http.get(`${PORTAL_BASE}/trash`, () => HttpResponse.json({ items: trash, total: trash.length, retention_days: 30 })),
  http.post(`${PORTAL_BASE}/trash/:kind/:id/restore`, ({ params }) => {
    trash = trash.filter((t) => t.id !== params.id);
    return HttpResponse.json({ status: "restored" });
  }),
  http.delete(`${PORTAL_BASE}/trash/:kind/:id`, ({ params }) => {
    trash = trash.filter((t) => t.id !== params.id);
    return HttpResponse.json({ status: "purged" });
  }),
  http.get(`${PORTAL_BASE}/unused`, ({ request }) => {
    const days = Number(new URL(request.url).searchParams.get("days") ?? 90);
    return HttpResponse.json({ items: unused, total: unused.length, days });
  }),
  http.get(`${PORTAL_BASE}/duplicates`, () => HttpResponse.json({ groups: groups() })),
  http.post(`${PORTAL_BASE}/duplicates/dismissals`, async ({ request }) => {
    const body = (await request.json()) as { digest?: string };
    if (body.digest) dismissed.add(body.digest);
    return new HttpResponse(null, { status: 204 });
  }),
  http.delete(`${PORTAL_BASE}/duplicates/dismissals/:digest`, ({ params }) => {
    dismissed.delete(String(params.digest));
    return new HttpResponse(null, { status: 204 });
  }),
];
