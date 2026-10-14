import { useState } from "react";
import { Box, Building2, Search, Tag, type LucideIcon } from "lucide-react";
import { useDefinitions, type DefinitionKind } from "@/api/portal/catalog";
import { EmptyState } from "@/components/patterns/EmptyState";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Input } from "@/components/ui/input";
import { useDebounced } from "@/lib/useDebounced";
import type { CatalogSubTab } from "@/lib/entityRefs";
import { CreateDefinitionForm } from "./catalog/CreateDefinitionForm";
import { DefinitionDetail } from "./catalog/DefinitionDetail";
import { PageCapNotice, VocabCard } from "./catalog/governance";
import { AddButton, ListSkeleton } from "./catalog/primitives";
import { clearRefFromLocation, deepLinkedRef, KIND_NAMES, setRefInLocation } from "./catalog/utils";

// PAGE is how many entries the list reads at once.
const PAGE = 200;

// The flat definition lists: each tab, the kind it lists and its icon.
const LISTS: Record<"tags" | "domains" | "data_products", { kind: DefinitionKind; icon: LucideIcon; plural: string }> = {
  tags: { kind: "tag", icon: Tag, plural: "tags" },
  domains: { kind: "domain", icon: Building2, plural: "domains" },
  data_products: { kind: "data_product", icon: Box, plural: "data products" },
};

/**
 * DefinitionsTab lists one kind of definition (tags, domains or data products),
 * filters it by name, opens an entry to see what it means and what carries it,
 * and, for an editor, creates one.
 */
export function DefinitionsTab({
  sub,
  canEdit,
  onNavigate,
}: {
  sub: Extract<CatalogSubTab, "tags" | "domains" | "data_products">;
  canEdit: boolean;
  onNavigate?: (path: string) => void;
}) {
  const { kind, icon, plural } = LISTS[sub];
  const [ref, setRef] = useState<string | null>(() => deepLinkedRef(sub));
  const [query, setQuery] = useState("");
  const [creating, setCreating] = useState(false);
  const debounced = useDebounced(query, 250);
  const list = useDefinitions({ kind: [kind], q: debounced.trim(), limit: PAGE }, !ref);
  const open = (next: string) => {
    setRef(next);
    setRefInLocation(next);
  };

  if (ref) {
    return (
      <DefinitionDetail
        key={ref}
        reference={ref}
        backLabel={`Back to ${plural}`}
        onNavigate={onNavigate}
        onBack={() => {
          setRef(null);
          clearRefFromLocation();
        }}
      />
    );
  }

  const entries = list.data ?? [];
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-2">
        <div className="relative min-w-60 flex-1">
          <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            aria-label={`Filter ${plural}`}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder={`Filter ${plural} by name…`}
            className="pl-9"
          />
        </div>
        {canEdit && !creating && (
          <AddButton label={`New ${KIND_NAMES[kind]}`} onClick={() => setCreating(true)} />
        )}
      </div>
      {creating && (
        <CreateDefinitionForm
          kinds={[kind]}
          onCancel={() => setCreating(false)}
          onDone={(def) => {
            setCreating(false);
            open(def.reference);
          }}
        />
      )}
      {list.isError ? (
        <Alert variant="destructive">
          <AlertDescription>Failed to load the {plural}.</AlertDescription>
        </Alert>
      ) : list.isLoading ? (
        <ListSkeleton />
      ) : entries.length === 0 ? (
        <EmptyState>{debounced.trim() ? `No ${plural} match.` : `No ${plural} have been defined.`}</EmptyState>
      ) : (
        <>
          <ul className="grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
            {entries.map((e) => (
              <li key={e.reference}>
                <VocabCard entry={e} icon={icon} onOpen={() => open(e.reference)} />
              </li>
            ))}
          </ul>
          <PageCapNotice shown={entries.length} limit={PAGE} what={plural} hint="Filter by name to find the rest." />
        </>
      )}
    </div>
  );
}
