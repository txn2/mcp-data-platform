// Mock data + stateful store for the portal DataHub Catalog and Context Docs
// endpoints (#719/#720), used by the MSW handlers and the interactive E2E suite.
import type {
  DataHubConnection,
  TableSearchResult,
  CatalogEntity,
  ContextDocument,
} from "@/api/portal/datahub";

export const mockDataHubConnections: DataHubConnection[] = [
  { name: "primary", writable: true },
  { name: "warehouse-ro", writable: false },
];

const urn = (name: string) => `urn:li:dataset:(urn:li:dataPlatform:trino,${name},PROD)`;

// A small stateful catalog so edits persist across reads within a session.
const catalog: Record<string, CatalogEntity> = {
  [urn("analytics.public.daily_sales")]: {
    urn: urn("analytics.public.daily_sales"),
    context: {
      urn: urn("analytics.public.daily_sales"),
      description: "Daily aggregated sales by store and product category.",
      owners: [{ urn: "urn:li:corpuser:sarah.chen", type: "TECHNICAL_OWNER", name: "Sarah Chen" }],
      tags: ["urn:li:tag:certified", "urn:li:tag:finance"],
      glossary_terms: [{ urn: "urn:li:glossaryTerm:Revenue", name: "Revenue" }],
      domain: { urn: "urn:li:domain:finance", name: "Finance" },
    },
    columns: {
      sale_date: { name: "sale_date", description: "Calendar date of the sale." },
      store_id: { name: "store_id", description: "Store identifier.", tags: ["urn:li:tag:key"] },
      revenue: { name: "revenue", description: "Gross revenue in USD.", is_sensitive: true },
      customer_email: { name: "customer_email", description: "Customer email.", is_pii: true },
    },
  },
  [urn("analytics.public.customers")]: {
    urn: urn("analytics.public.customers"),
    context: {
      urn: urn("analytics.public.customers"),
      description: "Customer master with contact and lifecycle fields.",
      owners: [],
      tags: ["urn:li:tag:pii"],
      glossary_terms: [],
      domain: null,
    },
    columns: {
      customer_id: { name: "customer_id", description: "Primary key." },
      email: { name: "email", description: "Email address.", is_pii: true },
    },
  },
  [urn("raw.events.clickstream")]: {
    urn: urn("raw.events.clickstream"),
    context: {
      urn: urn("raw.events.clickstream"),
      description: "Raw clickstream events ingested from the web tier.",
      owners: [],
      tags: [],
      glossary_terms: [],
      domain: null,
    },
    columns: {},
  },
};

function searchResult(e: CatalogEntity): TableSearchResult {
  const name = e.urn.match(/,([^,]+),PROD\)/)?.[1] ?? e.urn;
  return {
    urn: e.urn,
    name,
    platform: "trino",
    description: e.context?.description,
    tags: e.context?.tags,
    domain: e.context?.domain?.name,
  };
}

export function catalogBrowse(): TableSearchResult[] {
  return Object.values(catalog).map(searchResult);
}

export function catalogSearch(q: string): TableSearchResult[] {
  const needle = q.toLowerCase();
  return catalogBrowse().filter(
    (r) =>
      r.name.toLowerCase().includes(needle) ||
      (r.description ?? "").toLowerCase().includes(needle),
  );
}

export function catalogEntity(entityUrn: string): CatalogEntity | undefined {
  return catalog[entityUrn];
}

// applyCatalogChange mutates the in-memory entity so edits reflect on re-read.
export function applyCatalogChange(
  field: string,
  body: {
    urn: string;
    description?: string;
    add?: string[];
    remove?: string[];
    add_owners?: { owner_urn: string; ownership_type?: string }[];
    domain?: string;
    clear_domain?: boolean;
  },
): boolean {
  const e = catalog[body.urn];
  if (!e || !e.context) return false;
  const ctx = e.context;
  switch (field) {
    case "description":
      ctx.description = body.description ?? "";
      break;
    case "tags":
      ctx.tags = mergeStrings(ctx.tags ?? [], body.add, body.remove);
      break;
    case "glossary-terms": {
      const cur = new Set((ctx.glossary_terms ?? []).map((g) => g.urn));
      (body.remove ?? []).forEach((u) => cur.delete(u));
      (body.add ?? []).forEach((u) => cur.add(u));
      ctx.glossary_terms = [...cur].map((u) => ({ urn: u, name: u.split(":").pop() ?? u }));
      break;
    }
    case "owners": {
      const cur = (ctx.owners ?? []).filter((o) => !(body.remove ?? []).includes(o.urn));
      (body.add_owners ?? []).forEach((o) =>
        cur.push({ urn: o.owner_urn, type: o.ownership_type ?? "TECHNICAL_OWNER", name: o.owner_urn.split(":").pop() }),
      );
      ctx.owners = cur;
      break;
    }
    case "domain":
      ctx.domain = body.clear_domain || !body.domain
        ? null
        : { urn: body.domain, name: body.domain.split(":").pop() ?? body.domain };
      break;
    default:
      return false;
  }
  return true;
}

function mergeStrings(current: string[], add?: string[], remove?: string[]): string[] {
  const set = new Set(current);
  (remove ?? []).forEach((x) => set.delete(x));
  (add ?? []).forEach((x) => set.add(x));
  return [...set];
}

// --- context documents (stateful) ---

let docSeq = 2;
const documents: Record<string, ContextDocument> = {
  "doc-1": {
    urn: "urn:li:document:doc-1",
    title: "Daily sales refresh runbook",
    sub_type: "runbook",
    body: "# Daily sales refresh\n\nThe `daily_sales` table refreshes at 06:00 UTC via the `sales_agg` job.\n\n- Upstream: `raw.events.orders`\n- On failure, re-run the job and backfill the affected partition.",
    show_in_global_context: true,
    related_asset_urns: [urn("analytics.public.daily_sales")],
  },
  "doc-2": {
    urn: "urn:li:document:doc-2",
    title: "Revenue definition",
    sub_type: "note",
    body: "Revenue is **gross** and excludes refunds. See the Finance glossary for the certified definition.",
    show_in_global_context: true,
    related_asset_urns: ["urn:li:glossaryTerm:Revenue"],
  },
};

export function docsBrowse(): { documents: ContextDocument[]; total: number } {
  const list = Object.values(documents);
  return { documents: list, total: list.length };
}

export function docsSearch(q: string): ContextDocument[] {
  const needle = q.toLowerCase();
  return Object.values(documents).filter(
    (d) => d.title.toLowerCase().includes(needle) || (d.body ?? "").toLowerCase().includes(needle),
  );
}

export function getDoc(id: string): ContextDocument | undefined {
  return documents[id.replace(/^urn:li:document:/, "")];
}

export function createDoc(body: { entity_urn?: string; title: string; content: string; category?: string }): ContextDocument {
  docSeq += 1;
  const id = `doc-${docSeq}`;
  const doc: ContextDocument = {
    urn: `urn:li:document:${id}`,
    title: body.title,
    sub_type: body.category,
    body: body.content,
    show_in_global_context: true,
    related_asset_urns: body.entity_urn ? [body.entity_urn] : [],
  };
  documents[id] = doc;
  return doc;
}

export function updateDoc(id: string, body: { title: string; content: string; category?: string }): ContextDocument | undefined {
  const key = id.replace(/^urn:li:document:/, "");
  const doc = documents[key];
  if (!doc) return undefined;
  doc.title = body.title;
  doc.body = body.content;
  doc.sub_type = body.category;
  return doc;
}

export function deleteDoc(id: string): boolean {
  const key = id.replace(/^urn:li:document:/, "");
  if (!documents[key]) return false;
  delete documents[key];
  return true;
}
