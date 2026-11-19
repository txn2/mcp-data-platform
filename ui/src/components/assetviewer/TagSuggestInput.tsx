import { useId, useState } from "react";
import { Plus } from "lucide-react";
import { usePortalTags, type TagRow } from "@/api/portal/hooks/bulk";
import { Input } from "@/components/ui/input";

/** The tags in a comma-separated field, trimmed, blanks dropped. */
export function splitTags(value: string): string[] {
  return value
    .split(",")
    .map((t) => t.trim())
    .filter(Boolean);
}

/** The fragment being typed: what follows the last comma. */
export function currentFragment(value: string): string {
  const i = value.lastIndexOf(",");
  return (i < 0 ? value : value.slice(i + 1)).trim();
}

/** value with the fragment being typed replaced by tag, ready for the next one. */
export function completeWith(value: string, tag: string): string {
  const i = value.lastIndexOf(",");
  const head = i < 0 ? "" : `${value.slice(0, i + 1).trimEnd()} `;
  return `${head}${tag}, `;
}

/**
 * The suggestions for a fragment: existing tags beginning with it, without
 * case, that the field does not already carry, most carried first.
 */
export function suggestionsFor(rows: TagRow[], value: string): TagRow[] {
  const cut = value.lastIndexOf(",");
  const done = new Set(splitTags(cut < 0 ? "" : value.slice(0, cut)).map((t) => t.toLowerCase()));
  const frag = currentFragment(value).toLowerCase();
  return rows.filter((r) => {
    const t = r.tag.toLowerCase();
    return t.startsWith(frag) && !done.has(t);
  });
}

/**
 * A comma-separated tag field that suggests existing tags (#2508): the tags
 * on the assets the reader can open beginning with what they are typing,
 * without case, each with how many of those assets carry it, ending with the
 * typed tag offered as a new one. Choosing an existing tag uses its spelling,
 * so a library keeps one "finance" rather than gaining a "Finance".
 */
export function TagSuggestInput({
  id,
  value,
  onChange,
  placeholder,
  autoFocus,
}: {
  id?: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  autoFocus?: boolean;
}) {
  const listId = useId();
  const [open, setOpen] = useState(false);
  const fragment = currentFragment(value);
  const { data } = usePortalTags(fragment, open);
  const rows = suggestionsFor(data?.tags ?? [], value).slice(0, 6);
  const exact = rows.some((r) => r.tag.toLowerCase() === fragment.toLowerCase());
  const offerNew = fragment !== "" && !exact;
  const showList = open && (rows.length > 0 || offerNew);

  function choose(tag: string) {
    onChange(completeWith(value, tag));
  }

  return (
    <div className="relative">
      <Input
        id={id}
        type="text"
        value={value}
        placeholder={placeholder}
        autoFocus={autoFocus}
        autoComplete="off"
        role="combobox"
        aria-expanded={showList}
        aria-controls={listId}
        aria-autocomplete="list"
        onFocus={() => setOpen(true)}
        onBlur={() => setOpen(false)}
        onChange={(e) => {
          onChange(e.target.value);
          setOpen(true);
        }}
      />
      {showList && (
        <ul
          id={listId}
          role="listbox"
          aria-label="Existing tags"
          className="absolute z-20 mt-1 max-h-64 w-full overflow-auto rounded-md border bg-popover p-1 text-sm shadow-md"
        >
          {rows.map((r) => (
            <li key={r.tag} role="option" aria-selected={false}>
              <button
                type="button"
                className="flex min-h-9 w-full items-center justify-between gap-2 rounded px-2 text-left hover:bg-muted"
                // mousedown, so the choice lands before the input's blur closes the list
                onMouseDown={(e) => {
                  e.preventDefault();
                  choose(r.tag);
                }}
              >
                <span className="truncate">{r.tag}</span>
                <span className="shrink-0 text-xs text-muted-foreground">
                  {r.count} {r.count === 1 ? "asset" : "assets"}
                </span>
              </button>
            </li>
          ))}
          {offerNew && (
            <li role="option" aria-selected={false}>
              <button
                type="button"
                className="flex min-h-9 w-full items-center gap-2 rounded px-2 text-left text-muted-foreground hover:bg-muted"
                onMouseDown={(e) => {
                  e.preventDefault();
                  choose(fragment);
                }}
              >
                <Plus className="size-3.5" />
                Add &lsquo;{fragment}&rsquo; as a new tag
              </button>
            </li>
          )}
        </ul>
      )}
    </div>
  );
}
