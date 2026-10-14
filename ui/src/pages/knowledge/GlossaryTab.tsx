import { useState } from "react";
import { BookMarked, Folder, Search } from "lucide-react";
import { useDefinitions } from "@/api/portal/catalog";
import { EmptyState } from "@/components/patterns/EmptyState";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Input } from "@/components/ui/input";
import { useDebounced } from "@/lib/useDebounced";
import { CreateDefinitionForm } from "./catalog/CreateDefinitionForm";
import { DefinitionDetail } from "./catalog/DefinitionDetail";
import { PageCapNotice, VocabCard } from "./catalog/governance";
import { AddButton, ListSkeleton } from "./catalog/primitives";
import { clearRefFromLocation, deepLinkedRef, setRefInLocation } from "./catalog/utils";

// PAGE is how many entries one read lists.
const PAGE = 200;

/**
 * GlossaryTab is the Glossary tab of the Catalog section (#1921): the terms
 * this organization defines and the nodes that organize them. It lists the
 * top of the glossary, or every term and node matching a filter, and opens an
 * entry: a node shows what is filed under it, a term which datasets and
 * columns carry it. An editor creates a term or node at the top here, and
 * under a node on its page.
 */
export function GlossaryTab({
  canEdit,
  onNavigate,
}: {
  canEdit: boolean;
  onNavigate?: (path: string) => void;
}) {
  const [ref, setRef] = useState<string | null>(() => deepLinkedRef("glossary"));
  const [query, setQuery] = useState("");
  const [creating, setCreating] = useState(false);
  const debounced = useDebounced(query, 250).trim();
  const list = useDefinitions(
    { kind: ["glossary_node", "glossary_term"], roots: debounced === "", q: debounced, limit: PAGE },
    !ref,
  );
  const open = (next: string) => {
    setRef(next);
    setRefInLocation(next);
  };

  if (ref) {
    return (
      <DefinitionDetail
        key={ref}
        reference={ref}
        backLabel="Back to the glossary"
        onOpen={open}
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
            aria-label="Filter the glossary"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Filter terms and nodes by name…"
            className="pl-9"
          />
        </div>
        {canEdit && !creating && <AddButton label="New entry" onClick={() => setCreating(true)} />}
      </div>
      {creating && (
        <CreateDefinitionForm
          kinds={["glossary_term", "glossary_node"]}
          onCancel={() => setCreating(false)}
          onDone={(def) => {
            setCreating(false);
            open(def.reference);
          }}
        />
      )}
      {list.isError ? (
        <Alert variant="destructive">
          <AlertDescription>Failed to load the glossary.</AlertDescription>
        </Alert>
      ) : list.isLoading ? (
        <ListSkeleton />
      ) : entries.length === 0 ? (
        <EmptyState>
          {debounced ? "No terms or nodes match." : "The glossary is empty."}
        </EmptyState>
      ) : (
        <>
          <ul className="grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
            {entries.map((e) => (
              <li key={e.reference}>
                <VocabCard
                  entry={e}
                  icon={e.kind === "glossary_node" ? Folder : BookMarked}
                  onOpen={() => open(e.reference)}
                  trailing={
                    e.kind === "glossary_node" && (
                      <span className="ml-auto text-xs font-normal text-muted-foreground">
                        {e.children} {e.children === 1 ? "entry" : "entries"}
                      </span>
                    )
                  }
                />
              </li>
            ))}
          </ul>
          <PageCapNotice shown={entries.length} limit={PAGE} what="entries" hint="Filter by name to find the rest." />
        </>
      )}
    </div>
  );
}
