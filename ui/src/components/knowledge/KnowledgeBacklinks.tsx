import { BookOpen } from "lucide-react";
import { useKnowledgeBacklinks } from "@/api/portal/hooks";

/**
 * KnowledgeBacklinks surfaces the knowledge pages that reference an entity (the
 * reverse lookup, #664 Phase 4) on that entity's view: "N knowledge pages
 * reference this", with each page title. It renders nothing when there are no
 * accessible referencing pages. Clicking a title navigates to the Knowledge area
 * when an onNavigate handler is provided.
 */
export function KnowledgeBacklinks({
  urn,
  onNavigate,
}: {
  urn: string;
  onNavigate?: (path: string) => void;
}) {
  const { data } = useKnowledgeBacklinks(urn);
  const pages = data?.pages ?? [];
  if (pages.length === 0) return null;

  return (
    <div className="rounded-lg border border-border bg-card p-3">
      <p className="mb-2 flex items-center gap-1.5 text-xs font-medium text-muted-foreground">
        <BookOpen className="h-3.5 w-3.5" />
        {pages.length} knowledge {pages.length === 1 ? "page references" : "pages reference"} this
      </p>
      <div className="flex flex-wrap gap-1.5">
        {pages.map((p) => (
          <button
            key={p.id}
            type="button"
            onClick={() => onNavigate?.("/knowledge#knowledge")}
            disabled={!onNavigate}
            className="rounded-md border border-border px-2 py-1 text-xs text-foreground hover:bg-muted disabled:cursor-default disabled:hover:bg-transparent"
          >
            {p.title}
          </button>
        ))}
      </div>
    </div>
  );
}
