// Platform catalog API (#1921): typed hooks over the portal catalog routes at
// /api/v1/portal/catalog/*. Reading takes the persona's fetch grant and writing
// its apply_knowledge grant; the routes enforce both, and each read says
// whether the caller may write, so a page never decides that on its own.
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { apiFetch, apiFetchRaw, ApiError } from "./client";

// --- types (mirror pkg/catalog and internal/httpserver/catalogapi) ---

export type DefinitionKind =
  | "glossary_term"
  | "glossary_node"
  | "tag"
  | "domain"
  | "data_product";

export type AspectName =
  | "description"
  | "tags"
  | "glossary_terms"
  | "owners"
  | "domain"
  | "custom_properties"
  | "structured_properties"
  | "documentation_links"
  | "curated_queries"
  | "incidents"
  | "context_documents";

export interface Owner {
  owner: string;
  type?: string;
}

export interface DocLink {
  url: string;
  description?: string;
}

export interface CuratedQuery {
  name: string;
  sql: string;
  description?: string;
}

export interface Incident {
  id: string;
  type?: string;
  title: string;
  description?: string;
  state: string;
  resolution?: string;
}

export interface ContextDocument {
  id: string;
  title: string;
  content: string;
  content_type?: string;
  category?: string;
}

/** Aspects is the curation written on a dataset, column or definition. Tags
 * are tag names; glossary terms and the domain are mcp: references. */
export interface Aspects {
  description?: string;
  tags?: string[];
  glossary_terms?: string[];
  owners?: Owner[];
  domain?: string;
  custom_properties?: Record<string, string>;
  structured_properties?: Record<string, unknown[]>;
  documentation_links?: DocLink[];
  curated_queries?: CuratedQuery[];
  incidents?: Incident[];
  context_documents?: ContextDocument[];
}

export interface CatalogConnection {
  kind: string;
  name: string;
}

export interface DatasetSummary {
  reference: string;
  connection_kind: string;
  connection: string;
  path: string;
  name: string;
  type: string;
  description?: string;
  tags?: string[];
  domain?: string;
}

export interface Column {
  name: string;
  data_type?: string;
  ordinal: number;
  nullable: boolean;
  aspects: Aspects;
}

export interface Hop {
  source: string;
  target: string;
  writer_kind: string;
  writer_ref?: string;
  written_by?: string;
  written_at: string;
  depth: number;
}

export interface Dataset {
  id: string;
  reference: string;
  name: string;
  type: string;
  schema_resource_id?: string;
  schema_document?: string;
  columns: Column[];
  aspects: Aspects;
  removed_at?: string;
  created_at: string;
  updated_at: string;
  upstream: Hop[];
  downstream: Hop[];
  /** names maps each glossary term and domain reference to its display name. */
  names: Record<string, string>;
  writable: boolean;
}

export interface BrowseEntry {
  name: string;
  reference: string;
  /** How many datasets a path group holds; zero for a dataset. */
  datasets: number;
  type?: string;
}

export interface DefinitionSummary {
  reference: string;
  kind: DefinitionKind;
  slug: string;
  name: string;
  description?: string;
  parent?: string;
  /** How many entries sit directly under a glossary node. */
  children: number;
}

export interface Use {
  reference: string;
  dataset: string;
  name: string;
  column?: string;
  connection: string;
}

export interface Definition {
  id: string;
  reference: string;
  kind: DefinitionKind;
  slug: string;
  name: string;
  parent?: string;
  aspects: Aspects;
  created_at: string;
  updated_at: string;
}

export interface DefinitionView extends Definition {
  children: DefinitionSummary[];
  uses: Use[];
  uses_withheld: number;
  /** More datasets and columns carry it than are listed. */
  more_uses: boolean;
  writable: boolean;
}

export interface Change {
  id: string;
  reference: string;
  aspect: AspectName;
  version: number;
  previous?: unknown;
  value?: unknown;
  written_by: string;
  written_at: string;
  reverts?: string;
}

export interface SchemaDocument {
  id: string;
  reference: string;
  connection_kind: string;
  connection: string;
  filename: string;
  mime_type: string;
  size_bytes: number;
  version: number;
  updated_at: string;
  content: string;
}

// --- helpers ---

const enc = encodeURIComponent;

/** MIN_SEARCH_LEN is the shortest text a search is sent for. */
export const MIN_SEARCH_LEN = 2;

const keys = {
  all: ["catalog"] as const,
  connections: ["catalog", "connections"] as const,
  datasets: (p: DatasetSearch) => ["catalog", "datasets", p] as const,
  browse: (kind: string, conn: string, path: string) => ["catalog", "browse", kind, conn, path] as const,
  dataset: (ref: string) => ["catalog", "dataset", ref] as const,
  history: (ref: string) => ["catalog", "history", ref] as const,
  definitions: (p: DefinitionSearch) => ["catalog", "definitions", p] as const,
  definition: (ref: string) => ["catalog", "definition", ref] as const,
  schemaDocument: (ref: string) => ["catalog", "schema-document", ref] as const,
};

function query(params: Record<string, string | number | boolean | string[] | undefined>): string {
  const out = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v === undefined || v === "" || v === false) continue;
    if (Array.isArray(v)) v.forEach((x) => out.append(k, x));
    else out.set(k, String(v));
  }
  const s = out.toString();
  return s ? `?${s}` : "";
}

// --- reads ---

/** useCatalogConnections lists the connections the caller may browse, and
 * whether the caller may edit the catalog. */
export function useCatalogConnections() {
  return useQuery({
    queryKey: keys.connections,
    queryFn: () =>
      apiFetch<{ connections: CatalogConnection[]; writable: boolean }>("/catalog/connections"),
  });
}

export interface DatasetSearch {
  q?: string;
  connection_kind?: string;
  connection?: string;
  tag?: string[];
  limit?: number;
  offset?: number;
}

export function useDatasetSearch(p: DatasetSearch, enabled = true) {
  return useQuery({
    queryKey: keys.datasets(p),
    enabled,
    queryFn: () =>
      apiFetch<{ results: DatasetSummary[] }>(`/catalog/datasets${query({ ...p })}`).then(
        (r) => r.results ?? [],
      ),
  });
}

/** useCatalogBrowse reads one level of a connection's datasets. */
export function useCatalogBrowse(kind: string, conn: string, path: string) {
  return useQuery({
    queryKey: keys.browse(kind, conn, path),
    enabled: !!kind && !!conn,
    queryFn: () =>
      apiFetch<{ entries: BrowseEntry[] }>(
        `/catalog/browse${query({ connection_kind: kind, connection: conn, path })}`,
      ).then((r) => r.entries ?? []),
  });
}

export function useDataset(ref: string | null) {
  return useQuery({
    queryKey: keys.dataset(ref ?? ""),
    enabled: !!ref,
    queryFn: () => apiFetch<Dataset>(`/catalog/dataset?ref=${enc(ref ?? "")}`),
  });
}

export function useCatalogHistory(ref: string | null) {
  return useQuery({
    queryKey: keys.history(ref ?? ""),
    enabled: !!ref,
    queryFn: () =>
      apiFetch<{ changes: Change[] }>(`/catalog/history?ref=${enc(ref ?? "")}`).then(
        (r) => r.changes ?? [],
      ),
  });
}

export interface DefinitionSearch {
  kind?: DefinitionKind[];
  parent?: string;
  roots?: boolean;
  q?: string;
  limit?: number;
  offset?: number;
}

export function useDefinitions(p: DefinitionSearch, enabled = true) {
  return useQuery({
    queryKey: keys.definitions(p),
    enabled,
    queryFn: () =>
      apiFetch<{ results: DefinitionSummary[] }>(`/catalog/definitions${query({ ...p })}`).then(
        (r) => r.results ?? [],
      ),
  });
}

export function useDefinition(ref: string | null) {
  return useQuery({
    queryKey: keys.definition(ref ?? ""),
    enabled: !!ref,
    queryFn: () => apiFetch<DefinitionView>(`/catalog/definition?ref=${enc(ref ?? "")}`),
  });
}

export function useSchemaDocument(ref: string | null) {
  return useQuery({
    queryKey: keys.schemaDocument(ref ?? ""),
    enabled: !!ref,
    queryFn: () => apiFetch<SchemaDocument>(`/catalog/schema-document?ref=${enc(ref ?? "")}`),
  });
}

// --- writes ---

// Every write invalidates the whole catalog cache: an aspect written on a
// column changes its dataset, a tag added changes the datasets a tag search
// returns and the uses a definition lists, and the history of each.
function useInvalidateCatalog() {
  const qc = useQueryClient();
  return () => qc.invalidateQueries({ queryKey: keys.all });
}

export interface AspectWrite {
  reference: string;
  aspect: AspectName;
  /** The aspect's new value in its own shape; null clears it. */
  value: unknown;
  /** The value the edit was made from (null for absent). The write is refused
   * with 409 when the aspect has been written since. */
  expect?: unknown;
}

export function useWriteAspect() {
  const invalidate = useInvalidateCatalog();
  return useMutation({
    mutationFn: (w: AspectWrite) =>
      apiFetch<Change>("/catalog/aspect", { method: "PUT", body: JSON.stringify(w) }),
    onSuccess: invalidate,
  });
}

export interface RollbackRequest {
  reference: string;
  aspect: AspectName;
  change_id: string;
}

export function useRollback() {
  const invalidate = useInvalidateCatalog();
  return useMutation({
    mutationFn: (r: RollbackRequest) =>
      apiFetch<Change>("/catalog/rollback", { method: "POST", body: JSON.stringify(r) }),
    onSuccess: invalidate,
  });
}

export interface DefinitionCreate {
  kind: DefinitionKind;
  name: string;
  parent?: string;
  description?: string;
}

export function useCreateDefinition() {
  const invalidate = useInvalidateCatalog();
  return useMutation({
    mutationFn: (d: DefinitionCreate) =>
      apiFetch<Definition>("/catalog/definitions", { method: "POST", body: JSON.stringify(d) }),
    onSuccess: invalidate,
  });
}

export function useDeleteDefinition() {
  const invalidate = useInvalidateCatalog();
  return useMutation({
    mutationFn: async (ref: string) => {
      const res = await apiFetchRaw(`/catalog/definition?ref=${enc(ref)}`, { method: "DELETE" });
      if (!res.ok) {
        const body = await res.json().catch(() => ({ detail: res.statusText }));
        throw new ApiError(res.status, body.detail || res.statusText, body);
      }
    },
    onSuccess: invalidate,
  });
}
