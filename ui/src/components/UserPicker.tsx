import { useEffect, useRef, useState } from "react";
import { useDirectoryUsers } from "@/api/portal/hooks";
import type { DirectoryUser } from "@/api/portal/types";

interface Props {
  value: string;
  onChange: (email: string) => void;
  placeholder?: string;
}

function displayName(u: DirectoryUser): string {
  return [u.first_name, u.last_name].filter(Boolean).join(" ");
}

/**
 * UserPicker is an email input with type-ahead suggestions drawn from the
 * known-users directory (#614). It always accepts a free-typed email — the
 * directory only provides convenience suggestions, so sharing with someone not
 * yet in the directory still works. If the directory endpoint is unavailable
 * (no database), it degrades silently to a plain email input. The suggestion
 * list is keyboard navigable (Arrow keys / Enter / Escape) and exposed as an
 * ARIA listbox.
 */
export function UserPicker({ value, onChange, placeholder }: Props) {
  const [focused, setFocused] = useState(false);
  const [debounced, setDebounced] = useState(value);
  const [highlight, setHighlight] = useState(-1);
  const containerRef = useRef<HTMLDivElement>(null);

  // Debounce the directory query so we don't fetch on every keystroke.
  useEffect(() => {
    const t = setTimeout(() => setDebounced(value.trim()), 200);
    return () => clearTimeout(t);
  }, [value]);

  const { data } = useDirectoryUsers(debounced, focused);
  const suggestions = (data?.users ?? []).filter(
    (u) => u.email.toLowerCase() !== value.trim().toLowerCase(),
  );

  // Reset the keyboard highlight whenever the candidate set changes.
  useEffect(() => {
    setHighlight(-1);
  }, [debounced, data]);

  // Close the dropdown on outside click.
  useEffect(() => {
    if (!focused) return;
    function onClick(e: MouseEvent) {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) {
        setFocused(false);
      }
    }
    document.addEventListener("mousedown", onClick);
    return () => document.removeEventListener("mousedown", onClick);
  }, [focused]);

  const showDropdown = focused && suggestions.length > 0;

  function select(u: DirectoryUser) {
    onChange(u.email);
    setFocused(false);
    setHighlight(-1);
  }

  function onKeyDown(e: React.KeyboardEvent<HTMLInputElement>) {
    if (!showDropdown) return;
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setHighlight((h) => Math.min(h + 1, suggestions.length - 1));
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setHighlight((h) => Math.max(h - 1, 0));
    } else if (e.key === "Enter") {
      if (highlight >= 0 && highlight < suggestions.length) {
        e.preventDefault();
        select(suggestions[highlight]!);
      }
    } else if (e.key === "Escape") {
      setFocused(false);
    }
  }

  return (
    <div ref={containerRef} className="relative flex-1">
      <input
        type="email"
        placeholder={placeholder ?? "Email address"}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        onFocus={() => setFocused(true)}
        onKeyDown={onKeyDown}
        autoComplete="off"
        role="combobox"
        aria-expanded={showDropdown}
        aria-controls="user-picker-listbox"
        aria-activedescendant={highlight >= 0 ? `user-picker-opt-${highlight}` : undefined}
        className="w-full rounded-md border bg-background px-3 py-1.5 text-sm outline-none ring-ring focus:ring-2"
      />
      {showDropdown && (
        <ul
          id="user-picker-listbox"
          role="listbox"
          className="absolute z-50 mt-1 max-h-56 w-full overflow-auto rounded-md border bg-popover text-popover-foreground shadow-md"
        >
          {suggestions.map((u, i) => {
            const name = displayName(u);
            return (
              <li key={u.email} role="option" id={`user-picker-opt-${i}`} aria-selected={i === highlight}>
                <button
                  type="button"
                  // onMouseDown (not onClick) so the selection registers before
                  // the input's blur/outside-click handler closes the list.
                  onMouseDown={(e) => {
                    e.preventDefault();
                    select(u);
                  }}
                  onMouseEnter={() => setHighlight(i)}
                  className={`flex w-full items-center justify-between gap-2 px-3 py-1.5 text-left text-sm ${
                    i === highlight ? "bg-accent" : "hover:bg-accent"
                  }`}
                >
                  <span className="min-w-0 truncate">
                    {name ? (
                      <>
                        <span className="font-medium">{name}</span>{" "}
                        <span className="text-muted-foreground">{u.email}</span>
                      </>
                    ) : (
                      <span>{u.email}</span>
                    )}
                  </span>
                  {!u.confirmed && (
                    <span className="shrink-0 rounded-full bg-amber-100 px-1.5 py-0.5 text-xs font-medium text-amber-700 dark:bg-amber-900/30 dark:text-amber-400">
                      Invited
                    </span>
                  )}
                </button>
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}
