import { useMemo, useState } from "react";
import { X, Plus, Search } from "lucide-react";
import {
  useKnowledgePageRefs,
  useSetKnowledgePageRefs,
  useSearchAssets,
  useSearchCollections,
  useSearchKnowledgePages,
  useSearchMyPrompts,
  type PageEntityRef,
} from "@/api/portal/hooks";
import {
  useDataHubConnections,
  useDomainLookup,
  useGlossaryLookup,
  useTagLookup,
} from "@/api/portal/datahub";
import { filterDomains } from "@/pages/knowledge/catalog/utils";
import { buildRefUrn, isCatalogRefType, type PickableRefType } from "@/lib/entityRefs";
import { DataHubConnectionSelect } from "./DataHubConnectionSelect";
import { EntityChip } from "./EntityChip";

// PORTAL_TYPES are the entities the portal's own database holds. They are always
// offered; a deployment always has them.
const PORTAL_TYPES: { value: PickableRefType; label: string }[] = [
  { value: "asset", label: "Asset" },
  { value: "collection", label: "Collection" },
  { value: "knowledge_page", label: "Page" },
  { value: "prompt", label: "Prompt" },
];

// CATALOG_TYPES are the governance entities the DataHub catalog holds (#1159).
// They are offered only when a DataHub connection exists, because searching them
// is a read against one: a deployment with no catalog would otherwise show three
// types that can never return a candidate.
const CATALOG_TYPES: { value: PickableRefType; label: string }[] = [
  { value: "glossary_term", label: "Glossary term" },
  { value: "tag", label: "Tag" },
  { value: "domain", label: "Domain" },
];

// Candidate is one searchable target: what to show, and the reference URN
// picking it stores. The URN is carried rather than rebuilt from an id because
// a catalog entity's URN is its identity, not a key an id-based builder could
// reassemble.
interface Candidate {
  urn: string;
  label: string;
}

// portalCandidates maps one portal search's rows to candidates, serializing each
// row's id into the mcp: reference the picker stores.
function portalCandidates<T>(
  rows: T[] | undefined,
  type: PickableRefType,
  pick: (row: T) => [id: string, label: string],
): Candidate[] {
  return (rows ?? []).map((row) => {
    const [id, label] = pick(row);
    return { urn: buildRefUrn(type, id), label };
  });
}

// catalogCandidates maps catalog entity refs to candidates. A catalog entity is
// stored by its own URN, and an entry the catalog did not name falls back to
// that URN rather than rendering as a blank row.
function catalogCandidates(refs: { urn: string; name: string }[] | undefined): Candidate[] {
  return (refs ?? []).map((r) => ({ urn: r.urn, label: r.name || r.urn }));
}

/**
 * usePortalSearch runs only the selected portal type's search (the others get an
 * empty query, which disables them), normalizing results to a candidate.
 */
function usePortalSearch(type: PickableRefType, query: string): Candidate[] {
  const assets = useSearchAssets(type === "asset" ? query : "", { limit: 8 });
  const collections = useSearchCollections(type === "collection" ? query : "", { limit: 8 });
  const pages = useSearchKnowledgePages(type === "knowledge_page" ? query : "", { limit: 8 });
  const prompts = useSearchMyPrompts(type === "prompt" ? query : "", { limit: 8 });

  return useMemo(() => {
    const byType: Partial<Record<PickableRefType, Candidate[]>> = {
      asset: portalCandidates(assets.data?.data, "asset", (s) => [s.asset.id, s.asset.name]),
      collection: portalCandidates(collections.data?.data, "collection", (s) => [
        s.collection.id,
        s.collection.name,
      ]),
      knowledge_page: portalCandidates(pages.data, "knowledge_page", (s) => [
        s.page.id,
        s.page.title,
      ]),
      prompt: portalCandidates(prompts.data?.data, "prompt", (s) => [s.prompt.id, s.prompt.name]),
    };
    return byType[type] ?? [];
  }, [type, assets.data, collections.data, pages.data, prompts.data]);
}

/**
 * useCatalogSearch runs the selected governance type's lookup against a DataHub
 * connection. Terms and tags are name-searched upstream; domains have no
 * name-scoped search, so the whole (capped) list is read and filtered here —
 * the same asymmetry the Catalog tab's own pickers live with.
 */
function useCatalogSearch(type: PickableRefType, query: string, conn: string): Candidate[] {
  const terms = useGlossaryLookup(type === "glossary_term" ? conn : "", query);
  const tags = useTagLookup(type === "tag" ? conn : "", query);
  const domains = useDomainLookup(conn, type === "domain");

  return useMemo(() => {
    const byType: Partial<Record<PickableRefType, Candidate[]>> = {
      glossary_term: catalogCandidates(terms.data),
      tag: catalogCandidates(tags.data),
      domain: catalogCandidates(filterDomains(domains.data ?? [], query)).slice(0, CATALOG_MAX),
    };
    return byType[type] ?? [];
  }, [type, query, terms.data, tags.data, domains.data]);
}

// CATALOG_MAX bounds the client-filtered domain list to the same page the
// name-searched lookups return, so one type does not drop a hundred rows into
// the dropdown while the others show eight.
const CATALOG_MAX = 8;

/**
 * RefPicker manages a knowledge page's manually-authored references: it lists the
 * current manual refs (removable) and lets an editor search the portal's own
 * entity types and the DataHub governance vocabularies, and add one. Promoted and
 * inline references are preserved by the server-side source-scoped replace.
 *
 * A governance pick stores the entity's URN, which is why the search is by name:
 * an author attaches "Net Revenue" without ever seeing the generated key
 * DataHub gave that term.
 */
export function RefPicker({ pageId, onNavigate }: { pageId: string; onNavigate?: (path: string) => void }) {
  const { data } = useKnowledgePageRefs(pageId);
  const setRefs = useSetKnowledgePageRefs(pageId);
  const manual = useMemo<PageEntityRef[]>(
    () => (data?.refs ?? []).filter((r) => r.source === "manual"),
    [data],
  );
  const manualUrns = useMemo(() => manual.map((r) => r.urn), [manual]);

  const { data: connections } = useDataHubConnections();
  const hasCatalog = (connections?.length ?? 0) > 0;
  const types = useMemo(
    () => (hasCatalog ? [...PORTAL_TYPES, ...CATALOG_TYPES] : PORTAL_TYPES),
    [hasCatalog],
  );

  const [type, setType] = useState<PickableRefType>("asset");
  const [conn, setConn] = useState("");
  const [query, setQuery] = useState("");
  const trimmed = query.trim();
  const isCatalog = isCatalogRefType(type);
  const portalCandidates = usePortalSearch(type, isCatalog ? "" : trimmed);
  const catalogCandidates = useCatalogSearch(type, isCatalog ? trimmed : "", isCatalog ? conn : "");
  const candidates = isCatalog ? catalogCandidates : portalCandidates;

  const addRef = (urn: string) => {
    if (manualUrns.includes(urn)) return;
    setRefs.mutate([...manualUrns, urn]);
    setQuery("");
  };
  const removeRef = (urn: string) => setRefs.mutate(manualUrns.filter((u) => u !== urn));

  const selectedType = types.find((t) => t.value === type);

  return (
    <section className="rounded-lg border border-border bg-card p-4">
      <h2 className="mb-3 text-sm font-semibold text-foreground">Manual references</h2>

      <ManualRefs
        refs={manual}
        disabled={setRefs.isPending}
        onRemove={removeRef}
        onNavigate={onNavigate}
      />

      <div className="flex flex-wrap items-center gap-2">
        <select
          value={type}
          onChange={(e) => setType(e.target.value as PickableRefType)}
          aria-label="Reference type"
          className="rounded-md border border-border bg-background px-2 py-1.5 text-sm"
        >
          {types.map((t) => (
            <option key={t.value} value={t.value}>
              {t.label}
            </option>
          ))}
        </select>
        <div className="relative min-w-48 flex-1">
          <Search className="pointer-events-none absolute left-2 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
          <input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder={`Search ${(selectedType?.label ?? type).toLowerCase()}s to reference...`}
            className="w-full rounded-md border border-border bg-background py-1.5 pl-8 pr-2 text-sm"
          />
        </div>
        {/* The connection picker appears only for a catalog type: a governance
            entity belongs to one catalog, and the portal's own entities belong
            to none. */}
        {isCatalog && <DataHubConnectionSelect value={conn} onChange={setConn} />}
      </div>

      {trimmed.length > 0 && (
        <CandidateList
          candidates={candidates}
          existing={manualUrns}
          disabled={setRefs.isPending}
          onAdd={addRef}
        />
      )}
    </section>
  );
}

// ManualRefs lists the references an author added by hand, each removable. An
// empty set says so rather than rendering nothing, so the section reads as
// "none yet" instead of looking broken.
function ManualRefs({
  refs,
  disabled,
  onRemove,
  onNavigate,
}: {
  refs: PageEntityRef[];
  disabled: boolean;
  onRemove: (urn: string) => void;
  onNavigate?: (path: string) => void;
}) {
  if (refs.length === 0) {
    return <p className="mb-3 text-xs text-muted-foreground">No manual references yet.</p>;
  }
  return (
    <div className="mb-3 flex flex-wrap gap-1.5">
      {refs.map((ref) => (
        <span key={ref.urn} className="inline-flex items-center gap-1">
          <EntityChip
            urn={ref.urn}
            resolved={{ urn: ref.urn, type: ref.type, label: ref.label, exists: ref.exists, accessible: true }}
            onNavigate={onNavigate}
          />
          <button
            type="button"
            onClick={() => onRemove(ref.urn)}
            disabled={disabled}
            aria-label={`Remove ${ref.label}`}
            className="text-muted-foreground hover:text-destructive disabled:opacity-50"
          >
            <X className="h-3.5 w-3.5" />
          </button>
        </span>
      ))}
    </div>
  );
}

// CandidateList renders the search results, marking the ones already attached so
// a second click on the same entity is refused visibly rather than silently.
function CandidateList({
  candidates,
  existing,
  disabled,
  onAdd,
}: {
  candidates: Candidate[];
  existing: string[];
  disabled: boolean;
  onAdd: (urn: string) => void;
}) {
  return (
    <ul className="mt-2 max-h-56 overflow-y-auto rounded-md border border-border">
      {candidates.length === 0 ? (
        <li className="px-3 py-2 text-sm text-muted-foreground">No matches.</li>
      ) : (
        candidates.map((c) => {
          const already = existing.includes(c.urn);
          return (
            <li key={c.urn}>
              <button
                type="button"
                onClick={() => onAdd(c.urn)}
                disabled={already || disabled}
                className="flex w-full items-center justify-between px-3 py-2 text-left text-sm hover:bg-muted disabled:opacity-50"
              >
                <span className="truncate">{c.label}</span>
                {already ? (
                  <span className="text-xs text-muted-foreground">added</span>
                ) : (
                  <Plus className="h-4 w-4 shrink-0 text-muted-foreground" />
                )}
              </button>
            </li>
          );
        })
      )}
    </ul>
  );
}
