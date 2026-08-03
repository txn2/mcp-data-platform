import { useState } from "react";
import { Search } from "lucide-react";
import {
  useCatalogBrowse,
  useCatalogSearch,
  type TableSearchResult,
} from "@/api/portal/datahub";
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
        <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
        <input
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="Search tables by name, description, or tag…"
          className="w-full rounded-md border bg-background py-2 pl-9 pr-3 text-sm outline-none ring-ring focus:ring-2"
        />
      </div>

      {active.isError ? (
        <p className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">
          Failed to load the catalog.
        </p>
      ) : active.isLoading ? (
        <ListSkeleton />
      ) : results.length === 0 ? (
        <p className="rounded-md border border-dashed px-4 py-8 text-center text-sm text-muted-foreground">
          {searching ? "No tables match your search." : "No tables found in this connection."}
        </p>
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
