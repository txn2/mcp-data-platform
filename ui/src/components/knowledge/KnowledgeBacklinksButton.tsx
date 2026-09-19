import { useState } from "react";
import { BookOpen } from "lucide-react";
import { useKnowledgeBacklinks } from "@/api/portal/hooks";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { entityHref } from "@/lib/entityRefs";
import { backlinksSummary } from "./KnowledgeBacklinks";

/**
 * The knowledge pages that reference an entity, as a toolbar button (#1792).
 *
 * A viewer whose content is the page -- an asset, a collection -- has no room
 * for the full-width card KnowledgeBacklinks draws above it: the card pushed the
 * document down by the height of a toolbar to name one page. Here the count
 * rides on a button beside the viewer's other controls, and the pages open in a
 * modal, each one a way to that page.
 *
 * Renders nothing when no page the reader may see references the entity.
 */
export function KnowledgeBacklinksButton({
  urn,
  onNavigate,
}: {
  urn: string;
  onNavigate?: (path: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const { data } = useKnowledgeBacklinks(urn);
  const pages = data?.pages ?? [];
  if (pages.length === 0) return null;

  const summary = backlinksSummary(pages.length);
  return (
    <>
      <Button type="button" variant="outline" size="sm" onClick={() => setOpen(true)} title={summary}>
        <BookOpen />
        Referenced by
        <Badge className="px-1.5 text-[11px]">{pages.length}</Badge>
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>Referenced by</DialogTitle>
            <DialogDescription>{summary}.</DialogDescription>
          </DialogHeader>
          <ul className="max-h-[60vh] divide-y overflow-y-auto rounded-md border" data-testid="backlinks-list">
            {pages.map((p) => {
              const href = onNavigate ? entityHref("knowledge_page", p.id) : null;
              const face = (
                <>
                  <BookOpen className="size-4 shrink-0 text-muted-foreground" aria-hidden />
                  <span className="min-w-0 flex-1 truncate font-medium">{p.title}</span>
                  <span className="hidden shrink-0 truncate text-xs text-muted-foreground sm:inline">{p.slug}</span>
                </>
              );
              return (
                <li key={p.id}>
                  {href && onNavigate ? (
                    <button
                      type="button"
                      className="flex w-full items-center gap-2 px-3 py-2 text-left text-sm hover:bg-muted focus-visible:bg-muted focus-visible:outline-none"
                      onClick={() => {
                        setOpen(false);
                        onNavigate(href);
                      }}
                    >
                      {face}
                    </button>
                  ) : (
                    <div className="flex items-center gap-2 px-3 py-2 text-sm">{face}</div>
                  )}
                </li>
              );
            })}
          </ul>
        </DialogContent>
      </Dialog>
    </>
  );
}
