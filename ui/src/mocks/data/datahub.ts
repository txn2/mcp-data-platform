// Mock data + stateful store for the portal DataHub Catalog and Context Docs
// endpoints (#719/#720), used by the MSW handlers and the interactive E2E suite.
import type {
  DataHubConnection,
  TableSearchResult,
  CatalogEntity,
  ContextDocument,
  EntityRef,
} from "@/api/portal/datahub";

export const mockDataHubConnections: DataHubConnection[] = [
  { name: "primary", writable: true },
  { name: "warehouse-ro", writable: false },
];

const urn = (name: string) => `urn:li:dataset:(urn:li:dataPlatform:trino,${name},PROD)`;

// shortName mirrors the UI's shortUrn: the last URN segment as a display name.
const shortName = (u: string) => u.split(":").pop() ?? u;

// tagRefs derives the URN + name pairs the tag chips now read (mirrors the real
// backend's tag_refs, whose URN a governance write needs).
const tagRefs = (tags: string[]): EntityRef[] => tags.map((u) => ({ urn: u, name: shortName(u) }));

// Lookup fixtures for the metadata pickers (#785): name-searchable tags/terms/domains.
// The tag list is also the Tags governance surface's read (#1156), so it is
// mutable: a create appends and a delete removes.
const mockTags: EntityRef[] = [
  { urn: "urn:li:tag:certified", name: "certified", description: "Reviewed and approved by the data team." },
  { urn: "urn:li:tag:finance", name: "finance" },
  { urn: "urn:li:tag:pii", name: "pii", description: "Contains personally identifiable information." },
  { urn: "urn:li:tag:reviewed", name: "reviewed" },
];
const mockGlossaryTerms: EntityRef[] = [
  { urn: "urn:li:glossaryTerm:Revenue", name: "Revenue" },
  { urn: "urn:li:glossaryTerm:NetSales", name: "Net Sales" },
];
// The domain list is also the Domains governance surface's read (#1157), so it
// is mutable: a create appends and a delete removes.
const mockDomains: EntityRef[] = [
  { urn: "urn:li:domain:finance", name: "Finance", description: "Revenue, billing, and reporting." },
  { urn: "urn:li:domain:marketing", name: "Marketing" },
];

export function lookupTags(q: string): EntityRef[] {
  const needle = q.trim().toLowerCase();
  return mockTags.filter((t) => t.name.toLowerCase().includes(needle) || !needle);
}

// createTag appends a tag definition, mirroring the backend's 201 {urn} (#1156).
// DataHub derives the URN from the name.
export function createTag(name: string, description?: string): string {
  const tagUrn = `urn:li:tag:${name}`;
  if (!mockTags.some((t) => t.urn === tagUrn)) {
    mockTags.push({ urn: tagUrn, name, description });
  }
  return tagUrn;
}

// deleteTag removes a tag definition. It reports whether the tag existed so the
// handler can answer a delete of an unknown tag the way the backend would.
export function deleteTag(tagUrn: string): boolean {
  const i = mockTags.findIndex((t) => t.urn === tagUrn);
  if (i < 0) return false;
  mockTags.splice(i, 1);
  return true;
}

// setTagDescription applies an entity-description edit aimed at a tag URN, which
// is how the Tags surface edits a tag's description.
export function setTagDescription(tagUrn: string, description: string): boolean {
  const tag = mockTags.find((t) => t.urn === tagUrn);
  if (!tag) return false;
  tag.description = description;
  return true;
}

export function lookupGlossaryTerms(q: string): EntityRef[] {
  const needle = q.trim().toLowerCase();
  return mockGlossaryTerms.filter((t) => t.name.toLowerCase().includes(needle) || !needle);
}

export function lookupDomains(): EntityRef[] {
  return mockDomains;
}

// createDomain appends a domain definition, mirroring the backend's 201 {urn}
// (#1157). DataHub derives the URN from the name.
export function createDomain(name: string, description?: string): string {
  const domainUrn = `urn:li:domain:${name}`;
  if (!mockDomains.some((d) => d.urn === domainUrn)) {
    mockDomains.push({ urn: domainUrn, name, description });
  }
  return domainUrn;
}

// deleteDomain removes a domain definition. It reports whether the domain
// existed so the handler can answer a delete of an unknown domain the way the
// backend would. The tables that were in it keep their stored domain value, as
// upstream DeleteDomain touches only the domain entity.
export function deleteDomain(domainUrn: string): boolean {
  const i = mockDomains.findIndex((d) => d.urn === domainUrn);
  if (i < 0) return false;
  mockDomains.splice(i, 1);
  return true;
}

// setDomainDescription applies an entity-description edit aimed at a domain URN,
// which is how the Domains surface documents a domain.
export function setDomainDescription(domainUrn: string, description: string): boolean {
  const domain = mockDomains.find((d) => d.urn === domainUrn);
  if (!domain) return false;
  domain.description = description;
  return true;
}

// A small stateful catalog so edits persist across reads within a session.
const catalog: Record<string, CatalogEntity> = {
  [urn("analytics.public.daily_sales")]: {
    urn: urn("analytics.public.daily_sales"),
    context: {
      urn: urn("analytics.public.daily_sales"),
      description: "Daily aggregated sales by store and product category.",
      owners: [{ urn: "urn:li:corpuser:sarah.chen", type: "TECHNICAL_OWNER", name: "Sarah Chen" }],
      tags: ["certified", "finance"],
      tag_refs: tagRefs(["urn:li:tag:certified", "urn:li:tag:finance"]),
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
      tags: ["pii"],
      tag_refs: tagRefs(["urn:li:tag:pii"]),
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

// catalogSearch narrows the catalog by free text and, when given, by the tag
// URNs and the domain URN the caller filtered on. "*" is the match-all query the
// browse-style reads send (the Tags and Domains surfaces send it with a filter,
// where the filter and not the text selects the rows).
export function catalogSearch(q: string, tags: string[] = [], domain = ""): TableSearchResult[] {
  const needle = q.toLowerCase();
  const matchesText = (r: TableSearchResult) =>
    q === "*" ||
    r.name.toLowerCase().includes(needle) ||
    (r.description ?? "").toLowerCase().includes(needle);
  const matchesTags = (r: TableSearchResult) => {
    if (tags.length === 0) return true;
    const carried = new Set((catalogEntity(r.urn)?.context?.tag_refs ?? []).map((t) => t.urn));
    return tags.every((t) => carried.has(t));
  };
  // The search filter carries a domain URN; the result's own `domain` is the
  // display name, so the match is against the stored entity's domain URN.
  const matchesDomain = (r: TableSearchResult) =>
    !domain || catalogEntity(r.urn)?.context?.domain?.urn === domain;
  return catalogBrowse().filter((r) => matchesText(r) && matchesTags(r) && matchesDomain(r));
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
  // A description edit aimed at a tag or domain URN is how the Tags and Domains
  // surfaces document one: the backend's entity-description route takes any
  // entity URN, so the mock has to accept a non-dataset target too.
  if (field === "description" && body.urn.startsWith("urn:li:tag:")) {
    return setTagDescription(body.urn, body.description ?? "");
  }
  if (field === "description" && body.urn.startsWith("urn:li:domain:")) {
    return setDomainDescription(body.urn, body.description ?? "");
  }
  const e = catalog[body.urn];
  if (!e || !e.context) return false;
  const ctx = e.context;
  switch (field) {
    case "description":
      ctx.description = body.description ?? "";
      break;
    case "tags": {
      // add/remove carry tag URNs (the picker resolves names to URNs); tag_refs is
      // the source of truth and tags (names) is derived from it.
      const cur = new Set((ctx.tag_refs ?? []).map((t) => t.urn));
      (body.remove ?? []).forEach((u) => cur.delete(u));
      (body.add ?? []).forEach((u) => cur.add(u));
      ctx.tag_refs = tagRefs([...cur]);
      ctx.tags = [...cur].map(shortName);
      break;
    }
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
      // Resolve the display name from the domain vocabulary rather than from the
      // URN's last segment, so a table moved into "Finance" reads as Finance
      // (the URN id is lowercase) the way the real entity read returns it.
      ctx.domain =
        body.clear_domain || !body.domain
          ? null
          : {
              urn: body.domain,
              name:
                mockDomains.find((d) => d.urn === body.domain)?.name ??
                body.domain.split(":").pop() ??
                body.domain,
            };
      break;
    default:
      return false;
  }
  return true;
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
