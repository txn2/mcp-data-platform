import { Badge } from "@/components/ui/badge";
import { useAuthStore } from "@/stores/auth";
import { scopeIcon, scopeLabel } from "../shared";

// The resource library's two vocabularies — what a file is for (its category)
// and who it is for (its scope) — and the tint each word carries, so a resource
// reads the same colour in the reader's library and the admin table.

const CATEGORY_VARIANT: Record<string, React.ComponentProps<typeof Badge>["variant"]> = {
  // The primary fill rather than one of the semantic tints: the four below
  // took info/warning/secondary/success, and the ones left carry a status
  // meaning (danger reads as broken) that a category does not have. `visual`
  // takes the outline, the last neutral face that is not `muted` -- muted is
  // what a custom category falls back to, so a built-in wearing it would be
  // indistinguishable from one the deployment invented.
  data: "default",
  visual: "outline",
  samples: "info",
  playbooks: "warning",
  templates: "secondary",
  references: "success",
};

const SCOPE_VARIANT: Record<string, React.ComponentProps<typeof Badge>["variant"]> = {
  global: "info",
  persona: "warning",
};

// A deployment may define its own categories, which have no assigned tint; they
// fall back to muted rather than borrowing a built-in category's meaning.
// Both badges sit in a fixed-width table column, so a long word has to truncate
// with the whole of it still reachable on hover — a badge is `whitespace-nowrap`
// and the cell clips, so without this a longer persona name is cut mid-word
// with no way to read it.
export function CategoryBadge({ category }: { category: string }) {
  return (
    <Badge
      variant={CATEGORY_VARIANT[category] ?? "muted"}
      title={category}
      className="max-w-full px-1.5"
    >
      <span className="truncate">{category}</span>
    </Badge>
  );
}

export function ScopeBadge({ scope, scopeId }: { scope: string; scopeId: string }) {
  // The reader is read here rather than passed in because every caller is a list
  // row: threading it through the table and the grid would have both of them
  // carrying an identity neither uses for anything else.
  const viewer = useAuthStore((s) => s.user);
  const Icon = scopeIcon(scope);
  const label = scopeLabel(scope, scopeId, viewer);
  return (
    <Badge
      variant={SCOPE_VARIANT[scope] ?? "muted"}
      title={label}
      className="max-w-full px-1.5"
    >
      <Icon />
      <span className="truncate">{label}</span>
    </Badge>
  );
}
