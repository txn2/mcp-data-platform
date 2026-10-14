import { useMemo, useState } from "react";
import { X, Plus } from "lucide-react";
import {
  useKnowledgePageRefs,
  useSetKnowledgePageRefs,
  useSearchAssets,
  useSearchCollections,
  useSearchKnowledgePages,
  useSearchMyPrompts,
  type PageEntityRef,
} from "@/api/portal/hooks";
import { useCatalogConnections, useDefinitions, type DefinitionKind } from "@/api/portal/catalog";
import { buildRefUrn, isCatalogRefType, type PickableRefType } from "@/lib/entityRefs";
import { Button } from "@/components/ui/button";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { SearchInput } from "@/components/patterns/SearchInput";
import { SectionCard } from "@/components/patterns/SectionCard";
import { EntityChip } from "./EntityChip";

// PORTAL_TYPES are the entities the portal's own database holds. They are always
// offered; a deployment always has them.
const PORTAL_TYPES: { value: PickableRefType; label: string }[] = [
  { value: "asset", label: "Asset" },
  { value: "collection", label: "Collection" },
  { value: "knowledge_page", label: "Page" },
  { value: "prompt", label: "Prompt" },
];

// CATALOG_TYPES are the definitions the platform catalog holds (#1159). They are
// offered only to a reader who may read the catalog, since searching them is a
// catalog read: anyone else would see three types that can never return a
// candidate.
const CATALOG_TYPES: { value: PickableRefType; label: string }[] = [
  { value: "glossary_term", label: "Glossary term" },
  { value: "tag", label: "Tag" },
  { value: "domain", label: "Domain" },
];

// Candidate is one searchable target: what to show, and the reference picking
// it stores. The reference is carried rather than rebuilt from an id because a
// catalog definition's reference is the catalog's to give.
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
 * useCatalogSearch searches the selected definition kind in the platform
 * catalog by name.
 */
function useCatalogSearch(type: PickableRefType, query: string): Candidate[] {
  const enabled = isCatalogRefType(type) && query !== "";
  const defs = useDefinitions(
    { kind: [type as DefinitionKind], q: query, limit: CATALOG_MAX },
    enabled,
  );
  return useMemo(
    () => (enabled ? (defs.data ?? []).map((d) => ({ urn: d.reference, label: d.name })) : []),
    [enabled, defs.data],
  );
}

// useRefCandidates runs whichever of the two searches the selected type belongs
// to, holding the other at an empty query so only one read is in flight.
function useRefCandidates(type: PickableRefType, query: string): Candidate[] {
  const isCatalog = isCatalogRefType(type);
  const portal = usePortalSearch(type, isCatalog ? "" : query);
  const catalog = useCatalogSearch(type, isCatalog ? query : "");
  return isCatalog ? catalog : portal;
}

// CATALOG_MAX bounds a catalog search to the page the portal searches return.
const CATALOG_MAX = 8;

/**
 * RefPicker manages a knowledge page's manually-authored references: it lists the
 * current manual refs (removable) and lets an editor search the portal's own
 * entity types and the catalog's definitions, and add one. Promoted and
 * inline references are preserved by the server-side source-scoped replace.
 *
 * A definition pick stores its catalog reference, which is why the search is by
 * name: an author attaches "Net Revenue" without ever seeing the id the catalog
 * gave that term.
 */
export function RefPicker({ pageId, onNavigate }: { pageId: string; onNavigate?: (path: string) => void }) {
  const { data } = useKnowledgePageRefs(pageId);
  const setRefs = useSetKnowledgePageRefs(pageId);
  const manual = useMemo<PageEntityRef[]>(
    () => (data?.refs ?? []).filter((r) => r.source === "manual"),
    [data],
  );
  const manualUrns = useMemo(() => manual.map((r) => r.urn), [manual]);

  const hasCatalog = useCatalogConnections().isSuccess;
  const types = useMemo(
    () => (hasCatalog ? [...PORTAL_TYPES, ...CATALOG_TYPES] : PORTAL_TYPES),
    [hasCatalog],
  );

  const [type, setType] = useState<PickableRefType>("asset");
  const [query, setQuery] = useState("");
  const trimmed = query.trim();
  const candidates = useRefCandidates(type, trimmed);

  const addRef = (urn: string) => {
    if (manualUrns.includes(urn)) return;
    setRefs.mutate([...manualUrns, urn]);
    setQuery("");
  };
  const removeRef = (urn: string) => setRefs.mutate(manualUrns.filter((u) => u !== urn));

  return (
    <SectionCard title="Manual references">
      <ManualRefs
        refs={manual}
        disabled={setRefs.isPending}
        onRemove={removeRef}
        onNavigate={onNavigate}
      />

      <SearchBar
        types={types}
        type={type}
        onType={setType}
        query={query}
        onQuery={setQuery}
      />

      {trimmed.length > 0 && (
        <CandidateList
          candidates={candidates}
          existing={manualUrns}
          disabled={setRefs.isPending}
          onAdd={addRef}
        />
      )}
    </SectionCard>
  );
}

// SearchBar is what an author searches with: the kind of thing being attached
// and the name they are looking for.
function SearchBar({
  types,
  type,
  onType,
  query,
  onQuery,
}: {
  types: { value: PickableRefType; label: string }[];
  type: PickableRefType;
  onType: (t: PickableRefType) => void;
  query: string;
  onQuery: (q: string) => void;
}) {
  const label = types.find((t) => t.value === type)?.label ?? type;
  return (
    <div className="flex flex-wrap items-center gap-2">
      <Select value={type} onValueChange={(v) => onType(v as PickableRefType)}>
        <SelectTrigger size="sm" aria-label="Reference type">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {types.map((t) => (
            <SelectItem key={t.value} value={t.value}>
              {t.label}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      <SearchInput
        className="min-w-48 flex-1"
        inputClassName="h-8"
        value={query}
        onChange={(e) => onQuery(e.target.value)}
        placeholder={`Search ${label.toLowerCase()}s to reference...`}
      />
    </div>
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
          <Button
            type="button"
            variant="ghost"
            size="icon-xs"
            onClick={() => onRemove(ref.urn)}
            disabled={disabled}
            aria-label={`Remove ${ref.label}`}
            className="text-muted-foreground hover:text-destructive"
          >
            <X />
          </Button>
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
    <ul className="mt-2 max-h-56 overflow-y-auto rounded-md border">
      {candidates.length === 0 ? (
        <li className="px-3 py-2 text-sm text-muted-foreground">No matches.</li>
      ) : (
        candidates.map((c) => {
          const already = existing.includes(c.urn);
          return (
            <li key={c.urn}>
              <Button
                type="button"
                variant="ghost"
                onClick={() => onAdd(c.urn)}
                disabled={already || disabled}
                className="h-auto w-full justify-between rounded-none px-3 py-2 font-normal"
              >
                <span className="truncate">{c.label}</span>
                {already ? (
                  <span className="text-xs text-muted-foreground">added</span>
                ) : (
                  <Plus className="shrink-0 text-muted-foreground" />
                )}
              </Button>
            </li>
          );
        })
      )}
    </ul>
  );
}
