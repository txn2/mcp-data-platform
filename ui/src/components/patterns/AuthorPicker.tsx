import { ChevronDown } from "lucide-react";
import type { FacetCount } from "@/api/portal/types";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { cn } from "@/lib/utils";

/**
 * AuthorPicker is the Author filter (#2500): any number of the people who own
 * something the caller can open, each with how many of those items are
 * theirs. Nothing chosen means every author.
 */
export function AuthorPicker({
  authors,
  value,
  onChange,
  className,
}: {
  authors: FacetCount[];
  value: string[];
  onChange: (owners: string[]) => void;
  className?: string;
}) {
  const chosen = new Set(value);
  // A chosen author the current scope no longer lists stays offered, so it
  // can be cleared.
  const options = [
    ...authors,
    ...value.filter((v) => !authors.some((a) => a.value === v)).map((v) => ({ value: v, count: 0 })),
  ];
  const label =
    value.length === 0 ? "All authors" : value.length === 1 ? value[0] : `${value.length} authors`;

  function toggle(author: string, on: boolean) {
    onChange(on ? [...value, author] : value.filter((v) => v !== author));
  }

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          variant="outline"
          size="sm"
          aria-label="Filter by author"
          className={cn("justify-between text-xs font-normal", className)}
        >
          <span className="truncate">{label}</span>
          <ChevronDown className="size-3.5 opacity-60" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="max-h-80 overflow-y-auto">
        {options.length === 0 && (
          <DropdownMenuItem disabled className="text-xs">
            No authors
          </DropdownMenuItem>
        )}
        {options.map((a) => (
          <DropdownMenuCheckboxItem
            key={a.value}
            checked={chosen.has(a.value)}
            onCheckedChange={(on) => toggle(a.value, on === true)}
            onSelect={(e) => e.preventDefault()}
            className="text-xs"
          >
            <span className="truncate">{a.value}</span>
            {a.count > 0 && <span className="ml-auto pl-3 text-muted-foreground">{a.count}</span>}
          </DropdownMenuCheckboxItem>
        ))}
        {value.length > 0 && (
          <>
            <DropdownMenuSeparator />
            <DropdownMenuItem className="text-xs" onSelect={() => onChange([])}>
              Clear
            </DropdownMenuItem>
          </>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

/**
 * tagOptions is the Tag filter's choices: every tag on what the caller can
 * open in the scope, with its count, and the chosen one kept when the scope no
 * longer lists it.
 */
export function tagOptions(tags: FacetCount[], chosen: string) {
  const options = [
    { value: "", label: "All tags" },
    ...tags.map((t) => ({ value: t.value, label: `${t.value} (${t.count})` })),
  ];
  if (chosen && !tags.some((t) => t.value === chosen)) options.push({ value: chosen, label: chosen });
  return options;
}
