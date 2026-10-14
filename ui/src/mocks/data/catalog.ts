// Platform catalog fixtures (#1921): the datasets, definitions and curation
// history the portal catalog routes read and write. Mutable, so a write in a
// story or an e2e run is visible to the next read, as it is on a server.
import type {
  AspectName,
  Aspects,
  BrowseEntry,
  CatalogConnection,
  Change,
  Column,
  Dataset,
  DatasetSummary,
  DefinitionKind,
  DefinitionSummary,
  DefinitionView,
  Hop,
  SchemaDocument,
  Use,
} from "@/api/portal/catalog";

export const mockCatalogConnections: CatalogConnection[] = [
  { kind: "trino", name: "acme-warehouse" },
  { kind: "api", name: "billing" },
];

interface StoredDataset {
  reference: string;
  connection_kind: string;
  connection: string;
  path: string;
  name: string;
  type: string;
  schema_document?: string;
  columns: Column[];
  aspects: Aspects;
}

interface StoredDefinition {
  id: string;
  reference: string;
  kind: DefinitionKind;
  slug: string;
  name: string;
  parent?: string;
  aspects: Aspects;
}

const T0 = "2026-10-01T09:00:00Z";

const ds = (connection: string, path: string): string => `mcp:dataset:(trino,${connection},${path})`;

const col = (name: string, data_type: string, ordinal: number, aspects: Aspects = {}): Column => ({
  name,
  data_type,
  ordinal,
  nullable: ordinal > 1,
  aspects,
});

const definitions: StoredDefinition[] = [
  { id: "n1", reference: "mcp:glossary_node:n1", kind: "glossary_node", slug: "finance", name: "Finance", aspects: { description: "Revenue, billing and reporting vocabulary." } },
  { id: "n2", reference: "mcp:glossary_node:n2", kind: "glossary_node", slug: "billing", name: "Billing", parent: "mcp:glossary_node:n1", aspects: {} },
  { id: "t1", reference: "mcp:glossary_term:t1", kind: "glossary_term", slug: "revenue", name: "Revenue", parent: "mcp:glossary_node:n1", aspects: { description: "Gross sales before returns and discounts, in USD." } },
  { id: "t2", reference: "mcp:glossary_term:t2", kind: "glossary_term", slug: "net-sales", name: "Net Sales", aspects: { description: "Revenue less returns and discounts." } },
  { id: "pii", reference: "mcp:tag:pii", kind: "tag", slug: "pii", name: "pii", aspects: { description: "Contains personally identifiable information." } },
  { id: "certified", reference: "mcp:tag:certified", kind: "tag", slug: "certified", name: "certified", aspects: { description: "Reviewed and approved by the data team." } },
  { id: "finance", reference: "mcp:tag:finance", kind: "tag", slug: "finance", name: "finance", aspects: {} },
  { id: "d1", reference: "mcp:domain:d1", kind: "domain", slug: "sales", name: "Sales", aspects: { description: "Orders, stores and the revenue they bring in." } },
  { id: "d2", reference: "mcp:domain:d2", kind: "domain", slug: "marketing", name: "Marketing", aspects: {} },
  { id: "p1", reference: "mcp:data_product:p1", kind: "data_product", slug: "daily-revenue", name: "Daily revenue", aspects: { description: "The daily sales roll-up finance reports from." } },
];

const datasets: StoredDataset[] = [
  {
    reference: ds("acme-warehouse", "analytics.public.daily_sales"),
    connection_kind: "trino",
    connection: "acme-warehouse",
    path: "analytics.public.daily_sales",
    name: "daily_sales",
    type: "table",
    columns: [
      col("sale_date", "date", 1, { description: "Calendar date of the sale." }),
      col("store_id", "varchar", 2, { description: "Store identifier." }),
      col("revenue", "decimal(12,2)", 3, { description: "Gross revenue in USD.", glossary_terms: ["mcp:glossary_term:t1"] }),
      col("customer_email", "varchar", 4, { description: "Customer email.", tags: ["pii"] }),
    ],
    aspects: {
      description: "Daily aggregated sales by store and product category.",
      tags: ["certified", "finance"],
      glossary_terms: ["mcp:glossary_term:t1"],
      owners: [{ owner: "sarah.chen@example.com", type: "technical_owner" }],
      domain: "mcp:domain:d1",
      custom_properties: { refresh: "daily 02:00 UTC" },
      context_documents: [
        {
          id: "doc-1",
          title: "How revenue is counted",
          content: "Revenue is recognized on the **sale date**, not the ship date.",
          category: "definition",
        },
      ],
    },
  },
  {
    reference: ds("acme-warehouse", "analytics.public.customers"),
    connection_kind: "trino",
    connection: "acme-warehouse",
    path: "analytics.public.customers",
    name: "customers",
    type: "table",
    columns: [
      col("customer_id", "bigint", 1, { description: "Primary key." }),
      col("email", "varchar", 2, { description: "Email address.", tags: ["pii"] }),
    ],
    aspects: { description: "Customer master with contact and lifecycle fields.", tags: ["pii"] },
  },
  {
    reference: ds("acme-warehouse", "raw.events.clickstream"),
    connection_kind: "trino",
    connection: "acme-warehouse",
    path: "raw.events.clickstream",
    name: "clickstream",
    type: "table",
    columns: [col("event_id", "varchar", 1), col("occurred_at", "timestamp(3)", 2)],
    aspects: { description: "Raw clickstream events ingested from the web tier." },
  },
  {
    reference: "mcp:dataset:(api,billing,listInvoices)",
    connection_kind: "api",
    connection: "billing",
    path: "listInvoices",
    name: "listInvoices",
    type: "api_operation",
    schema_document: 'mcp:schema_document:(api,billing,"openapi.json")',
    columns: [col("id", "string", 1), col("amount_due", "integer", 2)],
    aspects: {},
  },
];

const lineage: Hop[] = [
  {
    source: ds("acme-warehouse", "raw.events.clickstream"),
    target: ds("acme-warehouse", "analytics.public.daily_sales"),
    writer_kind: "query",
    written_by: "etl@example.com",
    written_at: T0,
    depth: 1,
  },
];

const history: Change[] = [
  {
    id: "c1",
    reference: ds("acme-warehouse", "analytics.public.daily_sales"),
    aspect: "description",
    version: 1,
    value: "Daily aggregated sales by store and product category.",
    written_by: "sarah.chen@example.com",
    written_at: T0,
  },
];

const schemaDocument: SchemaDocument = {
  id: "sd1",
  reference: 'mcp:schema_document:(api,billing,"openapi.json")',
  connection_kind: "api",
  connection: "billing",
  filename: "openapi.json",
  mime_type: "application/json",
  size_bytes: 184_322,
  version: 3,
  updated_at: "2026-10-13T12:00:04Z",
  content: '{"openapi":"3.0.3","info":{"title":"Billing","version":"1"}}',
};

function summary(d: StoredDataset): DatasetSummary {
  return {
    reference: d.reference,
    connection_kind: d.connection_kind,
    connection: d.connection,
    path: d.path,
    name: d.name,
    type: d.type,
    description: d.aspects.description,
    tags: d.aspects.tags,
    domain: d.aspects.domain,
  };
}

export interface DatasetFilter {
  q?: string;
  connection_kind?: string;
  connection?: string;
  tags?: string[];
}

export function searchDatasets(f: DatasetFilter): DatasetSummary[] {
  const q = (f.q ?? "").trim().toLowerCase();
  return datasets
    .filter((d) => !f.connection_kind || d.connection_kind === f.connection_kind)
    .filter((d) => !f.connection || d.connection === f.connection)
    .filter((d) => (f.tags ?? []).every((t) => d.aspects.tags?.includes(t)))
    .filter(
      (d) =>
        q === "" ||
        q === "*" ||
        [d.name, d.path, d.aspects.description ?? ""].some((s) => s.toLowerCase().includes(q)),
    )
    .sort((a, b) => a.name.localeCompare(b.name))
    .map(summary);
}

export function browse(kind: string, connection: string, path: string): BrowseEntry[] {
  const prefix = path ? path.split(".") : [];
  const groups = new Map<string, number>();
  const out: BrowseEntry[] = [];
  for (const d of datasets) {
    if (d.connection_kind !== kind || d.connection !== connection) continue;
    const parts = d.path.split(".");
    if (prefix.some((p, i) => parts[i] !== p) || parts.length <= prefix.length) continue;
    if (parts.length === prefix.length + 1) {
      out.push({ name: d.name, reference: d.reference, datasets: 0, type: d.type });
    } else {
      const g = parts[prefix.length] ?? "";
      groups.set(g, (groups.get(g) ?? 0) + 1);
    }
  }
  for (const [name, count] of groups) {
    out.push({ name, reference: `mcp:dataset:(${kind},${connection},${[...prefix, name].join(".")})`, datasets: count });
  }
  return out.sort((a, b) => a.name.localeCompare(b.name));
}

function names(d: StoredDataset): Record<string, string> {
  const out: Record<string, string> = {};
  const refs = [...(d.aspects.glossary_terms ?? []), d.aspects.domain ?? ""];
  for (const c of d.columns) refs.push(...(c.aspects.glossary_terms ?? []));
  for (const r of refs) {
    const def = definitions.find((x) => x.reference === r);
    if (def) out[r] = def.name;
  }
  return out;
}

export function getDataset(ref: string): Dataset | undefined {
  const d = datasets.find((x) => x.reference === ref);
  if (!d) return undefined;
  return {
    id: d.reference,
    reference: d.reference,
    name: d.name,
    type: d.type,
    schema_document: d.schema_document,
    columns: d.columns,
    aspects: d.aspects,
    created_at: T0,
    updated_at: T0,
    upstream: lineage.filter((h) => h.target === d.reference),
    downstream: lineage.filter((h) => h.source === d.reference),
    names: names(d),
    writable: true,
  };
}

export function getSchemaDocument(ref: string): SchemaDocument | undefined {
  return ref === schemaDocument.reference ? schemaDocument : undefined;
}

// aspectsOf finds the curation a reference names: a dataset's, a column's or a
// definition's.
function aspectsOf(ref: string): Aspects | undefined {
  const column = ref.match(/^mcp:column:\((.*),([^,]+)\)$/);
  if (column) {
    const d = datasets.find((x) => x.reference === `mcp:dataset:(${column[1]})`);
    return d?.columns.find((c) => c.name === column[2])?.aspects;
  }
  return (datasets.find((x) => x.reference === ref) ?? definitions.find((x) => x.reference === ref))?.aspects;
}

export function historyOf(ref: string): Change[] {
  return history.filter((c) => c.reference === ref).sort((a, b) => b.written_at.localeCompare(a.written_at));
}

export function writeAspect(ref: string, aspect: AspectName, value: unknown, by: string, reverts?: string): Change | undefined {
  const a = aspectsOf(ref);
  if (!a) return undefined;
  const record = a as Record<string, unknown>;
  const previous = record[aspect];
  if (value === null || value === undefined) delete record[aspect];
  else record[aspect] = value;
  const change: Change = {
    id: `c${history.length + 1}`,
    reference: ref,
    aspect,
    version: history.filter((c) => c.reference === ref && c.aspect === aspect).length + 1,
    previous,
    value: value ?? undefined,
    written_by: by,
    written_at: new Date().toISOString(),
    reverts,
  };
  history.push(change);
  return change;
}

export function rollback(changeID: string, by: string): Change | undefined {
  const c = history.find((x) => x.id === changeID);
  if (!c) return undefined;
  return writeAspect(c.reference, c.aspect, c.previous ?? null, by, c.id);
}

function definitionSummary(d: StoredDefinition): DefinitionSummary {
  return {
    reference: d.reference,
    kind: d.kind,
    slug: d.slug,
    name: d.name,
    description: d.aspects.description,
    parent: d.parent,
    children: definitions.filter((x) => x.parent === d.reference).length,
  };
}

export interface DefinitionFilter {
  kinds: string[];
  parent?: string;
  roots?: boolean;
  q?: string;
}

export function listDefinitions(f: DefinitionFilter): DefinitionSummary[] {
  const q = (f.q ?? "").trim().toLowerCase();
  return definitions
    .filter((d) => f.kinds.length === 0 || f.kinds.includes(d.kind))
    .filter((d) => !f.parent || d.parent === f.parent)
    .filter((d) => !f.roots || !d.parent)
    .filter((d) => q === "" || [d.name, d.slug, d.aspects.description ?? ""].some((s) => s.toLowerCase().includes(q)))
    .sort((a, b) => a.name.localeCompare(b.name))
    .map(definitionSummary);
}

function usesOf(d: StoredDefinition): Use[] {
  const carries = (a: Aspects): boolean =>
    d.kind === "tag"
      ? (a.tags ?? []).includes(d.name)
      : d.kind === "glossary_term"
        ? (a.glossary_terms ?? []).includes(d.reference)
        : d.kind === "domain" && a.domain === d.reference;
  const out: Use[] = [];
  for (const ds of datasets) {
    if (carries(ds.aspects)) {
      out.push({ reference: ds.reference, dataset: ds.reference, name: ds.name, connection: ds.connection });
    }
    for (const c of ds.columns) {
      if (carries(c.aspects)) {
        out.push({
          reference: ds.reference.replace("mcp:dataset:(", "mcp:column:(").replace(/\)$/, `,${c.name})`),
          dataset: ds.reference,
          name: ds.name,
          column: c.name,
          connection: ds.connection,
        });
      }
    }
  }
  return out;
}

// findDefinition resolves a reference whose key may be the id or the slug.
function findDefinition(ref: string): StoredDefinition | undefined {
  const m = ref.match(/^mcp:([a-z_]+):(.+)$/);
  if (!m) return undefined;
  return definitions.find((d) => d.kind === m[1] && (d.id === m[2] || d.slug === m[2] || d.reference === ref));
}

export function getDefinition(ref: string): DefinitionView | undefined {
  const d = findDefinition(ref);
  if (!d) return undefined;
  return {
    id: d.id,
    reference: d.reference,
    kind: d.kind,
    slug: d.slug,
    name: d.name,
    parent: d.parent,
    aspects: d.aspects,
    created_at: T0,
    updated_at: T0,
    children: definitions.filter((x) => x.parent === d.reference).map(definitionSummary),
    uses: usesOf(d),
    uses_withheld: 0,
    more_uses: false,
    writable: true,
  };
}

/** existingDefinition is the definition a create of this kind and name would
 * land on, which the route refuses rather than overwrite. */
export function existingDefinition(kind: DefinitionKind, name: string): DefinitionView | undefined {
  const slug = kind === "tag" ? name : name.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "");
  const d = definitions.find((x) => x.kind === kind && x.slug === slug);
  return d ? getDefinition(d.reference) : undefined;
}

export function createDefinition(kind: DefinitionKind, name: string, parent?: string, description?: string): DefinitionView {
  const slug = kind === "tag" ? name : name.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "");
  let d = definitions.find((x) => x.kind === kind && x.slug === slug);
  if (!d) {
    const id = `${kind}-${definitions.length + 1}`;
    d = { id, reference: kind === "tag" ? `mcp:tag:${name}` : `mcp:${kind}:${id}`, kind, slug, name, parent, aspects: {} };
    definitions.push(d);
  }
  if (description) d.aspects.description = description;
  return getDefinition(d.reference) as DefinitionView;
}

/** deleteDefinition removes a definition, or names why it may not: what is
 * filed under it, or what carries it. */
export function deleteDefinition(ref: string): "deleted" | "missing" | string {
  const d = findDefinition(ref);
  if (!d) return "missing";
  const children = definitions.filter((x) => x.parent === d.reference).length;
  if (children > 0) return `${d.reference} has ${children} entries under it`;
  const uses = usesOf(d).length;
  if (uses > 0) return `${d.reference} is carried by ${uses} datasets or columns`;
  definitions.splice(definitions.indexOf(d), 1);
  return "deleted";
}
