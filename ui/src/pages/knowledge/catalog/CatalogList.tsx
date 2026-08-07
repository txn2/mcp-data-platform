import { useState } from "react";
import { Search } from "lucide-react";
import {
  useCatalogBrowse,
  useCatalogSearch,
  type TableSearchResult,
} from "@/api/portal/datahub";
import { EmptyState } from "@/components/patterns/EmptyState";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Input } from "@/components/ui/input";
import { markdownToPlainText } from "@/lib/markdownText";
import { useDebounced } from "@/lib/useDebounced";
import { MIN_SEARCH, shortUrn } from "./utils";
import { ListSkeleton } from "./primitives";

export function CatalogList({ conn, onOpen }: { conn: string; onOpen: (urn: string) => void }) {
  const [query, setQuery] = useState("");
  const debounced = useDebounced(query, 250);
  const searching = debounced.trim().length >= MIN_SEARCH;
  const browse = useCatalogBrowse(conn, { limit: 50 });
  const search = useCatalogSearch(conn, debounced, { limit: 25 });
  const active = searching ? search : browse;
  const results: TableSearchResult[] = active.data ?? [];

  return (
    <div className="space-y-4">
      <div className="relative">
        <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
        <Input
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="Search tables by name, description, or tag…"
          className="pl-9"
        />
      </div>

      {active.isError ? (
        <Alert variant="destructive">
          <AlertDescription>Failed to load the catalog.</AlertDescription>
        </Alert>
      ) : active.isLoading ? (
        <ListSkeleton />
      ) : results.length === 0 ? (
        <EmptyState>
          {searching ? "No tables match your search." : "No tables found in this connection."}
        </EmptyState>
      ) : (
        <ul className="grid gap-2 sm:grid-cols-2">
          {results.map((r) => (
            <li key={r.urn}>
              <button
                onClick={() => onOpen(r.urn)}
                className="flex w-full flex-col gap-1 rounded-lg border p-3 text-left transition-colors hover:border-primary/50 hover:bg-muted/50"
              >
                <span className="truncate text-sm font-medium">{r.name || r.urn}</span>
                {r.description && (
                  <span className="line-clamp-2 text-xs text-muted-foreground">
                    {markdownToPlainText(r.description)}
                  </span>
                )}
                <span className="mt-1 flex flex-wrap items-center gap-1.5">
                  {r.platform && (
                    <span className="rounded bg-muted px-1.5 py-0.5 text-[11px] text-muted-foreground">
                      {r.platform}
                    </span>
                  )}
                  {(r.tags ?? []).slice(0, 4).map((t) => (
                    <span key={t} className="rounded bg-primary/10 px-1.5 py-0.5 text-[11px] text-primary">
                      {shortUrn(t)}
                    </span>
                  ))}
                </span>
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
