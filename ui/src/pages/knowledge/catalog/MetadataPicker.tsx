import { useState } from "react";
import { Search, Plus } from "lucide-react";
import { MIN_SEARCH } from "./utils";

/** Candidate is one value a picker offers: the key written into the aspect
 * (a tag name, a definition reference) and the name shown for it. */
export interface Candidate {
  key: string;
  label: string;
  detail?: string;
}

/**
 * MetadataPicker is the shared typeahead used by the tag, glossary-term and
 * domain editors (#785): the user types a display name and picks a result, so
 * a raw reference is never typed. Selecting a candidate does not blur the input
 * (the list uses onMouseDown preventDefault), and the dropdown opens on focus
 * for a preloaded list (openOnFocus) or once the query reaches the search
 * threshold otherwise.
 */
export function MetadataPicker({
  placeholder,
  label,
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
  label: string;
  query: string;
  setQuery: (v: string) => void;
  candidates: Candidate[];
  loading: boolean;
  isPending: boolean;
  existingKeys: Set<string>;
  onPick: (c: Candidate) => void;
  openOnFocus?: boolean;
  emptyHint?: string;
}) {
  const [focused, setFocused] = useState(false);
  const open = focused && (openOnFocus || query.trim().length >= MIN_SEARCH);

  return (
    <div className="relative mt-2 w-72 max-w-full">
      <div className="relative">
        <Search className="pointer-events-none absolute left-2 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
        <input
          aria-label={label}
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          onFocus={() => setFocused(true)}
          onBlur={() => setFocused(false)}
          placeholder={placeholder}
          disabled={isPending}
          className="w-full rounded-md border bg-transparent py-1 pl-8 pr-2 text-xs outline-none ring-ring focus:ring-2 disabled:opacity-50 dark:bg-input/30"
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
              const already = existingKeys.has(c.key);
              return (
                <li key={c.key}>
                  <button
                    type="button"
                    disabled={already || isPending}
                    onClick={() => onPick(c)}
                    className="flex w-full items-center justify-between gap-2 px-3 py-1.5 text-left text-xs hover:bg-muted disabled:opacity-50"
                  >
                    <span className="truncate">
                      <span className="font-medium">{c.label}</span>
                      {c.detail && (
                        <span className="ml-1.5 text-[11px] text-muted-foreground">{c.detail}</span>
                      )}
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
