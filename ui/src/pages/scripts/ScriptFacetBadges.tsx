import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";

// ScriptFacetBadges is how a script is filed, wherever a script is shown: the
// one category it belongs to and the tags it carries (#1369).
//
// It is one component rather than a per-surface rendering because the three
// surfaces that show it — the owner's listing, the administrator's listing, and
// the script's own page — must agree on what filing looks like. A category read
// as a tag on one page and as a category on another is exactly the confusion
// two hand-rolled badge rows produce.
//
// The category is the filled badge and the tags are outlined, because they are
// not the same kind of value: a script has at most one category, chosen from a
// vocabulary the deployment converges on, and any number of free-form tags.
//
// Nothing renders when the script carries neither, rather than an empty strip.
export function ScriptFacetBadges({
  category,
  tags,
  className,
}: {
  category?: string;
  tags?: string[];
  className?: string;
}) {
  const carried = tags ?? [];
  if (!category && carried.length === 0) {
    return null;
  }
  return (
    <div className={cn("flex flex-wrap items-center gap-1.5", className)}>
      {category && <Badge variant="secondary">{category}</Badge>}
      {carried.map((tag) => (
        <Badge key={tag} variant="outline">
          {tag}
        </Badge>
      ))}
    </div>
  );
}
