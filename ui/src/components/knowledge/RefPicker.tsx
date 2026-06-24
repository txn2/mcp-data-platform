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
import { buildRefUrn, type PickableRefType } from "@/lib/entityRefs";
import { EntityChip } from "./EntityChip";

const TYPES: { value: PickableRefType; label: string }[] = [
  { value: "asset", label: "Asset" },
  { value: "collection", label: "Collection" },
  { value: "knowledge_page", label: "Page" },
  { value: "prompt", label: "Prompt" },
];

interface Candidate {
  id: string;
  label: string;
}

/**
 * useTypeSearch runs only the selected type's search (the others get an empty
 * query, which disables them), normalizing results to {id, label}.
 */
function useTypeSearch(type: PickableRefType, query: string): Candidate[] {
  const assets = useSearchAssets(type === "asset" ? query : "", { limit: 8 });
  const collections = useSearchCollections(type === "collection" ? query : "", { limit: 8 });
  const pages = useSearchKnowledgePages(type === "knowledge_page" ? query : "", { limit: 8 });
  const prompts = useSearchMyPrompts(type === "prompt" ? query : "", { limit: 8 });

  return useMemo(() => {
    switch (type) {
      case "asset":
        return (assets.data?.data ?? []).map((s) => ({ id: s.asset.id, label: s.asset.name }));
      case "collection":
        return (collections.data?.data ?? []).map((s) => ({ id: s.collection.id, label: s.collection.name }));
      case "knowledge_page":
        return (pages.data ?? []).map((s) => ({ id: s.page.id, label: s.page.title }));
      case "prompt":
        return (prompts.data?.data ?? []).map((s) => ({ id: s.prompt.id, label: s.prompt.name }));
    }
  }, [type, assets.data, collections.data, pages.data, prompts.data]);
}

/**
 * RefPicker manages a knowledge page's manually-authored references: it lists the
 * current manual refs (removable) and lets an editor search the four portal entity
 * types and add one. Promoted and inline references are preserved by the
 * server-side source-scoped replace.
 */
export function RefPicker({ pageId, onNavigate }: { pageId: string; onNavigate?: (path: string) => void }) {
  const { data } = useKnowledgePageRefs(pageId);
  const setRefs = useSetKnowledgePageRefs(pageId);
  const manual = useMemo<PageEntityRef[]>(
    () => (data?.refs ?? []).filter((r) => r.source === "manual"),
    [data],
  );
  const manualUrns = useMemo(() => manual.map((r) => r.urn), [manual]);

  const [type, setType] = useState<PickableRefType>("asset");
  const [query, setQuery] = useState("");
  const candidates = useTypeSearch(type, query.trim());

  const addRef = (id: string) => {
    const urn = buildRefUrn(type, id);
    if (manualUrns.includes(urn)) return;
    setRefs.mutate([...manualUrns, urn]);
    setQuery("");
  };
  const removeRef = (urn: string) => setRefs.mutate(manualUrns.filter((u) => u !== urn));

  return (
    <section className="rounded-lg border border-border bg-card p-4">
      <h2 className="mb-3 text-sm font-semibold text-foreground">Manual references</h2>

      {manual.length > 0 ? (
        <div className="mb-3 flex flex-wrap gap-1.5">
          {manual.map((ref) => (
            <span key={ref.urn} className="inline-flex items-center gap-1">
              <EntityChip
                urn={ref.urn}
                resolved={{ urn: ref.urn, type: ref.type, label: ref.label, exists: ref.exists, accessible: true }}
                onNavigate={onNavigate}
              />
              <button
                type="button"
                onClick={() => removeRef(ref.urn)}
                disabled={setRefs.isPending}
                aria-label={`Remove ${ref.label}`}
                className="text-muted-foreground hover:text-destructive disabled:opacity-50"
              >
                <X className="h-3.5 w-3.5" />
              </button>
            </span>
          ))}
        </div>
      ) : (
        <p className="mb-3 text-xs text-muted-foreground">No manual references yet.</p>
      )}

      <div className="flex items-center gap-2">
        <select
          value={type}
          onChange={(e) => setType(e.target.value as PickableRefType)}
          className="rounded-md border border-border bg-background px-2 py-1.5 text-sm"
        >
          {TYPES.map((t) => (
            <option key={t.value} value={t.value}>
              {t.label}
            </option>
          ))}
        </select>
        <div className="relative flex-1">
          <Search className="pointer-events-none absolute left-2 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
          <input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder={`Search ${type.replace("_", " ")}s to reference...`}
            className="w-full rounded-md border border-border bg-background py-1.5 pl-8 pr-2 text-sm"
          />
        </div>
      </div>

      {query.trim().length > 0 && (
        <ul className="mt-2 max-h-56 overflow-y-auto rounded-md border border-border">
          {candidates.length === 0 ? (
            <li className="px-3 py-2 text-sm text-muted-foreground">No matches.</li>
          ) : (
            candidates.map((c) => {
              const already = manualUrns.includes(buildRefUrn(type, c.id));
              return (
                <li key={c.id}>
                  <button
                    type="button"
                    onClick={() => addRef(c.id)}
                    disabled={already || setRefs.isPending}
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
      )}
    </section>
  );
}
