import { useMemo, useState } from "react";
import { Check, ChevronsUpDown, User, X } from "lucide-react";
import { useDirectoryUsers } from "@/api/admin/hooks";
import type { DirectoryUser } from "@/api/admin/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";

// displayName is how a person reads in the picker: their name when the
// directory has one, otherwise the address it knows them by.
function displayName(user: DirectoryUser): string {
  const full = `${user.first_name} ${user.last_name}`.trim();
  return full || user.email;
}

// BindToUser picks the account a key is issued against (#1759). A key bound to
// somebody authenticates as them, so the picker offers only people the platform
// has actually seen sign in: a directory row an admin pre-added has no subject
// to present and no roles to carry, and a key bound to it would authenticate as
// nobody.
export function BindToUser({
  value,
  onChange,
  onPick,
  disabled,
}: {
  /** The address the key is bound to, or "" for a standalone service key. */
  value: string;
  onChange: (email: string) => void;
  /** Called with the person picked, so the form can fill its roles from them. */
  onPick: (user: DirectoryUser | null) => void;
  disabled?: boolean;
}) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const { data, isLoading } = useDirectoryUsers(query || undefined);

  // Only people the platform has seen sign in can hold a key: the rest have no
  // recorded identity for one to resolve through.
  const people = useMemo(
    () => (data?.users ?? []).filter((u) => u.confirmed),
    [data],
  );

  const selected = useMemo(
    () => people.find((u) => u.email === value) ?? null,
    [people, value],
  );

  if (value) {
    return (
      <div className="flex items-center gap-2 rounded-md border bg-muted/30 px-3 py-2">
        <User className="size-4 shrink-0 text-muted-foreground" />
        <div className="min-w-0 flex-1">
          <div className="truncate text-sm font-medium">
            {selected ? displayName(selected) : value}
          </div>
          {selected && selected.email !== displayName(selected) && (
            <div className="truncate text-xs text-muted-foreground">{selected.email}</div>
          )}
        </div>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          disabled={disabled}
          onClick={() => {
            onChange("");
            onPick(null);
            setOpen(false);
          }}
          aria-label="Clear the account this key is issued against"
        >
          <X />
        </Button>
      </div>
    );
  }

  return (
    <div className="space-y-2">
      <Button
        type="button"
        variant="outline"
        size="sm"
        className="w-full justify-between font-normal"
        disabled={disabled}
        aria-expanded={open}
        onClick={() => setOpen((o) => !o)}
      >
        <span className="text-muted-foreground">
          Service key (not tied to a person)
        </span>
        <ChevronsUpDown className="size-4 opacity-50" />
      </Button>

      {open && (
        <div className="space-y-2 rounded-md border p-2">
          <Input
            type="search"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Search people by name or email"
            aria-label="Search people"
          />
          <div className="max-h-56 overflow-y-auto">
            {isLoading && (
              <p className="px-2 py-3 text-xs text-muted-foreground">Loading...</p>
            )}
            {!isLoading && people.length === 0 && (
              <p className="px-2 py-3 text-xs text-muted-foreground">
                Nobody here has signed in yet. A key can only be issued against an
                account the platform has seen sign in, because that is where the
                identity and roles it carries come from.
              </p>
            )}
            {people.map((user) => (
              <button
                key={user.email}
                type="button"
                className={cn(
                  "flex w-full items-center gap-2 rounded-sm px-2 py-1.5 text-left text-sm",
                  "hover:bg-accent hover:text-accent-foreground",
                )}
                onClick={() => {
                  onChange(user.email);
                  onPick(user);
                  setOpen(false);
                  setQuery("");
                }}
              >
                <Check
                  className={cn(
                    "size-4 shrink-0",
                    user.email === value ? "opacity-100" : "opacity-0",
                  )}
                />
                <span className="min-w-0 flex-1">
                  <span className="block truncate">{displayName(user)}</span>
                  <span className="block truncate text-xs text-muted-foreground">
                    {user.email}
                    {user.roles && user.roles.length > 0
                      ? ` - ${user.roles.join(", ")}`
                      : " - no roles recorded"}
                  </span>
                </span>
              </button>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}
