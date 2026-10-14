import { http, HttpResponse } from "msw";
import type { AspectWrite, DefinitionCreate, DefinitionKind, RollbackRequest } from "@/api/portal/catalog";
import {
  browse,
  createDefinition,
  existingDefinition,
  deleteDefinition,
  getDataset,
  getDefinition,
  getSchemaDocument,
  historyOf,
  listDefinitions,
  mockCatalogConnections,
  rollback,
  searchDatasets,
  writeAspect,
} from "../data/catalog";

// The portal catalog routes (#1921), over the fixtures in ../data/catalog.
const BASE = "/api/v1/portal/catalog";
const WRITER = "admin@example.com";

const notFound = (what: string) => HttpResponse.json({ detail: `${what} not found` }, { status: 404 });

export const platformCatalogHandlers = [
  http.get(`${BASE}/connections`, () =>
    HttpResponse.json({ connections: mockCatalogConnections, writable: true }),
  ),
  http.get(`${BASE}/datasets`, ({ request }) => {
    const q = new URL(request.url).searchParams;
    return HttpResponse.json({
      results: searchDatasets({
        q: q.get("q") ?? "",
        connection_kind: q.get("connection_kind") ?? "",
        connection: q.get("connection") ?? "",
        tags: q.getAll("tag"),
      }),
    });
  }),
  http.get(`${BASE}/browse`, ({ request }) => {
    const q = new URL(request.url).searchParams;
    return HttpResponse.json({
      entries: browse(q.get("connection_kind") ?? "", q.get("connection") ?? "", q.get("path") ?? ""),
    });
  }),
  http.get(`${BASE}/dataset`, ({ request }) => {
    const d = getDataset(new URL(request.url).searchParams.get("ref") ?? "");
    return d ? HttpResponse.json(d) : notFound("dataset");
  }),
  http.get(`${BASE}/schema-document`, ({ request }) => {
    const d = getSchemaDocument(new URL(request.url).searchParams.get("ref") ?? "");
    return d ? HttpResponse.json(d) : notFound("schema document");
  }),
  http.get(`${BASE}/history`, ({ request }) =>
    HttpResponse.json({ changes: historyOf(new URL(request.url).searchParams.get("ref") ?? "") }),
  ),
  http.put(`${BASE}/aspect`, async ({ request }) => {
    const body = (await request.json()) as AspectWrite;
    const change = writeAspect(body.reference, body.aspect, body.value, WRITER);
    return change ? HttpResponse.json(change) : notFound("entity");
  }),
  http.post(`${BASE}/rollback`, async ({ request }) => {
    const body = (await request.json()) as RollbackRequest;
    const change = rollback(body.change_id, WRITER);
    return change ? HttpResponse.json(change) : notFound("change");
  }),
  http.get(`${BASE}/definitions`, ({ request }) => {
    const q = new URL(request.url).searchParams;
    return HttpResponse.json({
      results: listDefinitions({
        kinds: q.getAll("kind"),
        parent: q.get("parent") ?? "",
        roots: q.get("roots") === "true",
        q: q.get("q") ?? "",
      }),
    });
  }),
  http.post(`${BASE}/definitions`, async ({ request }) => {
    const body = (await request.json()) as DefinitionCreate;
    const existing = existingDefinition(body.kind, body.name);
    if (existing) {
      return HttpResponse.json(
        { detail: `a ${body.kind.replace("_", " ")} named "${existing.name}" already exists (${existing.reference}); open it to edit it` },
        { status: 409 },
      );
    }
    return HttpResponse.json(
      createDefinition(body.kind as DefinitionKind, body.name, body.parent, body.description),
      { status: 201 },
    );
  }),
  http.get(`${BASE}/definition`, ({ request }) => {
    const d = getDefinition(new URL(request.url).searchParams.get("ref") ?? "");
    return d ? HttpResponse.json(d) : notFound("definition");
  }),
  http.delete(`${BASE}/definition`, ({ request }) => {
    const out = deleteDefinition(new URL(request.url).searchParams.get("ref") ?? "");
    if (out === "deleted") return new HttpResponse(null, { status: 204 });
    if (out === "missing") return notFound("definition");
    return HttpResponse.json({ detail: out }, { status: 409 });
  }),
];
