import { BookOpen } from "lucide-react";
import { useKnowledgeBacklinks } from "@/api/portal/hooks";
import { SectionCard } from "@/components/patterns/SectionCard";
import { EntityChip } from "./EntityChip";

/**
 * KnowledgeBacklinks surfaces the knowledge pages that reference an entity (the
 * reverse lookup, #664 Phase 4) on that entity's view: "N knowledge pages
 * reference this", with each page title. It renders nothing when there are no
 * accessible referencing pages.
 *
 * Each backlink is an EntityChip (#709), so inbound references look and behave
 * exactly like the outbound references in the Related panel: clicking a page
 * opens that specific page's detail (via its knowledge_page route), not a generic
 * anchor. This makes the reference graph wiki-navigable in both directions.
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
    <SectionCard
      title={
        <span className="flex items-center gap-1.5 text-muted-foreground">
          <BookOpen className="size-3.5" aria-hidden />
          {pages.length} knowledge {pages.length === 1 ? "page references" : "pages reference"} this
        </span>
      }
    >
      <div className="flex flex-wrap gap-1.5">
        {pages.map((p) => {
          const pageUrn = `mcp:knowledge_page:${p.id}`;
          return (
            <EntityChip
              key={p.id}
              urn={pageUrn}
              resolved={{ urn: pageUrn, type: "knowledge_page", label: p.title, exists: true, accessible: true }}
              onNavigate={onNavigate}
            />
          );
        })}
      </div>
    </SectionCard>
  );
}
