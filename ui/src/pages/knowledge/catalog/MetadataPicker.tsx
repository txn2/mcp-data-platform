import { useState } from "react";
import { Search, Plus } from "lucide-react";
import { type EntityRef } from "@/api/portal/datahub";
import { MIN_SEARCH, shortUrn } from "./utils";

/**
 * MetadataPicker is the shared typeahead used by the tag, glossary, and domain
 * editors (#785): the user types a display name and picks a result that resolves
 * to a URN, so raw URN entry is never required. Selecting a candidate does not
 * blur the input (the list uses onMouseDown preventDefault), and the dropdown
 * opens on focus for a preloaded list (openOnFocus) or once the query reaches the
 * search threshold otherwise.
 */
export function MetadataPicker({
  placeholder,
  query,
  setQuery,
  candidates,
  loading,
  isPending,
  existingKeys,
  onPick,
  openOnFocus = false,
  emptyHint = "No matches.",
}: {
  placeholder: string;
  query: string;
  setQuery: (v: string) => void;
  candidates: EntityRef[];
  loading: boolean;
  isPending: boolean;
  existingKeys: Set<string>;
  onPick: (ref: EntityRef) => void;
  openOnFocus?: boolean;
  emptyHint?: string;
}) {
  const [focused, setFocused] = useState(false);
  const open = focused && (openOnFocus || query.trim().length >= MIN_SEARCH);

  return (
    <div className="relative mt-2 w-72">
      <div className="relative">
        <Search className="pointer-events-none absolute left-2 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
        <input
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          onFocus={() => setFocused(true)}
          onBlur={() => setFocused(false)}
          placeholder={placeholder}
          disabled={isPending}
          className="w-full rounded-md border bg-background py-1 pl-8 pr-2 text-xs outline-none ring-ring focus:ring-2 disabled:opacity-50"
        />
      </div>
      {open && (
        <ul
          // Keep focus on the input so a click selects instead of blurring first.
          onMouseDown={(e) => e.preventDefault()}
          className="absolute z-10 mt-1 max-h-56 w-full overflow-y-auto rounded-md border bg-popover text-popover-foreground shadow-md"
        >
          {candidates.length === 0 ? (
            <li className="px-3 py-2 text-xs text-muted-foreground">
              {loading ? "Searching…" : emptyHint}
            </li>
          ) : (
            candidates.map((c) => {
              const already = existingKeys.has(c.urn);
              return (
                <li key={c.urn}>
                  <button
                    type="button"
                    disabled={already || isPending}
                    onClick={() => onPick(c)}
                    className="flex w-full items-center justify-between gap-2 px-3 py-1.5 text-left text-xs hover:bg-muted disabled:opacity-50"
                  >
                    <span className="truncate">
                      <span className="font-medium">{c.name || shortUrn(c.urn)}</span>
                      <span className="ml-1.5 font-mono text-[11px] text-muted-foreground">
                        {shortUrn(c.urn)}
                      </span>
                    </span>
                    {already ? (
                      <span className="shrink-0 text-muted-foreground">added</span>
                    ) : (
                      <Plus className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
                    )}
                  </button>
                </li>
              );
            })
          )}
        </ul>
      )}
    </div>
  );
}
