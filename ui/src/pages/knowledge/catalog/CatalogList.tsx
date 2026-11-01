import { useState } from "react";
import { Search } from "lucide-react";
import { useCatalogConnections, useDatasetSearch, type DatasetSummary } from "@/api/portal/catalog";
import { EmptyState } from "@/components/patterns/EmptyState";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { markdownToPlainText } from "@/lib/markdownText";
import { useDebounced } from "@/lib/useDebounced";
import { PageCapNotice } from "./governance";
import { ListSkeleton } from "./primitives";
import { byName } from "@/lib/sortByName";

// PAGE is how many datasets the list reads at once.
const PAGE = 50;

// ALL is the connection filter's value for every connection.
const ALL = "*";

// splitConnection reads the filter's kind/name value: a kind holds no slash, a
// name may. ALL, the empty filter, is no connection.
function splitConnection(value: string): [string, string] {
  const slash = value.indexOf("/");
  if (value === ALL || slash < 0) return ["", ""];
  return [value.slice(0, slash), value.slice(slash + 1)];
}

/**
 * CatalogList finds datasets across every connection the caller's persona
 * reaches: by text in the name, path or description, optionally on one
 * connection. An empty search lists datasets by name.
 */
export function CatalogList({ onOpen }: { onOpen: (ref: string) => void }) {
  const [query, setQuery] = useState("");
  const [conn, setConn] = useState(ALL);
  const debounced = useDebounced(query, 250);
  const connections = useCatalogConnections();
  const [kind, name] = splitConnection(conn);
  const search = useDatasetSearch({
    q: debounced.trim(),
    connection_kind: kind,
    connection: name,
    limit: PAGE,
  });
  const results: DatasetSummary[] = search.data ?? [];
  const conns = [...(connections.data?.connections ?? [])].sort(byName((c) => c.name));

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap gap-2">
        <div className="relative min-w-60 flex-1">
          <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            aria-label="Search tables"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Search tables by name, path, or description…"
            className="pl-9"
          />
        </div>
        {conns.length > 1 && (
          <Select value={conn} onValueChange={setConn}>
            <SelectTrigger aria-label="Connection" className="w-56">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={ALL}>All connections</SelectItem>
              {conns.map((c) => (
                <SelectItem key={`${c.kind}/${c.name}`} value={`${c.kind}/${c.name}`}>
                  {c.name} ({c.kind})
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        )}
      </div>

      {search.isError ? (
        <Alert variant="destructive">
          <AlertDescription>Failed to load the catalog.</AlertDescription>
        </Alert>
      ) : search.isLoading ? (
        <ListSkeleton />
      ) : results.length === 0 ? (
        <EmptyState>
          {debounced.trim()
            ? "No tables match your search."
            : "The catalog holds no tables yet. A catalog scan of each connection fills it."}
        </EmptyState>
      ) : (
        <>
          <ul className="grid gap-2 sm:grid-cols-2">
            {results.map((r) => (
              <li key={r.reference}>
                <button
                  onClick={() => onOpen(r.reference)}
                  className="flex w-full flex-col gap-1 rounded-lg border p-3 text-left transition-colors hover:border-primary/50 hover:bg-muted/50"
                >
                  <span className="truncate text-sm font-medium">{r.name}</span>
                  <span className="truncate font-mono text-[11px] text-muted-foreground">{r.path}</span>
                  {r.description && (
                    <span className="line-clamp-2 text-xs text-muted-foreground">
                      {markdownToPlainText(r.description)}
                    </span>
                  )}
                  <span className="mt-1 flex flex-wrap items-center gap-1.5">
                    <span className="rounded bg-muted px-1.5 py-0.5 text-[11px] text-muted-foreground">
                      {r.connection}
                    </span>
                    {(r.tags ?? []).slice(0, 4).map((t) => (
                      <span key={t} className="rounded bg-primary/10 px-1.5 py-0.5 text-[11px] text-primary">
                        {t}
                      </span>
                    ))}
                  </span>
                </button>
              </li>
            ))}
          </ul>
          <PageCapNotice
            shown={results.length}
            limit={PAGE}
            what="tables"
            hint="Narrow the search to find the rest."
          />
        </>
      )}
    </div>
  );
}
