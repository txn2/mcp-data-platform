import { useMemo, useState } from "react";
import { ChevronRight } from "lucide-react";
import { cn } from "@/lib/utils";
import type { Resource } from "@/api/resources/types";
import { CATEGORY_HINTS } from "../shared";
import { CategoryBadge } from "./badges";
import { groupByCategory, readCollapsed, writeCollapsed, type ResourceGroup } from "./groups";
import { ResourceGrid } from "./ResourceGrid";
import { ResourcesTable } from "./ResourcesTable";

/**
 * The library, one collapsible section per category in view.
 *
 * The header states what the section holds while it is folded as well as
 * while it is open — its name, what the category is for, and how many
 * resources are in it — so folding hides the list and never the fact that the
 * list is there.
 */
export function ResourceGroups({
  resources,
  admin,
  complete,
  onOpen,
}: {
  resources: Resource[];
  admin: boolean;
  /**
   * True when every resource in this library has been loaded. The sections are
   * built from the pages fetched so far, so until it is, a count is how many
   * have arrived rather than how many the category holds — and a category
   * whose members all sort past the loaded pages has no section at all.
   */
  complete: boolean;
  onOpen: (resource: Resource) => void;
}) {
  const [collapsed, setCollapsed] = useState<string[]>(readCollapsed);
  const groups = useMemo(() => groupByCategory(resources), [resources]);

  const toggle = (category: string) => {
    const next = collapsed.includes(category)
      ? collapsed.filter((c) => c !== category)
      : [...collapsed, category];
    setCollapsed(next);
    writeCollapsed(next);
  };

  return (
    <div className="space-y-4">
      {groups.map((group) => (
        <CategorySection
          key={group.category}
          group={group}
          admin={admin}
          complete={complete}
          open={!collapsed.includes(group.category)}
          onToggle={() => toggle(group.category)}
          onOpen={onOpen}
        />
      ))}
    </div>
  );
}

function CategorySection({
  group,
  admin,
  complete,
  open,
  onToggle,
  onOpen,
}: {
  group: ResourceGroup;
  admin: boolean;
  complete: boolean;
  open: boolean;
  onToggle: () => void;
  onOpen: (resource: Resource) => void;
}) {
  const hint = CATEGORY_HINTS[group.category];
  const count = group.resources.length;
  return (
    <section>
      <button
        type="button"
        data-testid={`resource-group-toggle-${group.category}`}
        aria-expanded={open}
        onClick={onToggle}
        className="flex w-full min-w-0 items-baseline gap-2 py-1 text-left"
      >
        <ChevronRight
          aria-hidden
          className={cn(
            "size-3.5 shrink-0 self-center text-muted-foreground transition-transform",
            open && "rotate-90",
          )}
        />
        {/* The category badge rather than plain text: the tint each category
            wears is the library's own vocabulary, and it now appears once per
            section instead of once per row. */}
        <h3 className="shrink-0 text-sm font-medium">
          <CategoryBadge category={group.category} />
        </h3>
        {/* A count off a partly-loaded library is how many have arrived, not
            how many there are, and a bare number would not say so. */}
        <span
          className="shrink-0 text-xs text-muted-foreground tabular-nums"
          title={complete ? undefined : "At least this many; more load as you scroll"}
        >
          {complete ? count : `${count}+`}
        </span>
        {hint && <span className="truncate text-xs text-muted-foreground">{hint}</span>}
      </button>
      {open &&
        (group.images ? (
          <ResourceGrid resources={group.resources} admin={admin} onOpen={onOpen} />
        ) : (
          <ResourcesTable resources={group.resources} admin={admin} onOpen={onOpen} />
        ))}
    </section>
  );
}
