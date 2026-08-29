import { CornerDownRight, File } from "lucide-react";
import { Button } from "@/components/ui/button";
import { markdownToPlainText } from "@/lib/markdownText";
import type { Resource } from "@/api/resources/types";
import { ScopeBadge } from "./badges";

/**
 * What a search over the whole library found, each hit showing where it is.
 *
 * A search spans the library rather than the open folder, which is what stops
 * the tree being worse than the flat list it replaced -- but a hit with no path
 * on it is then a file the reader cannot place, so the folder is on the row and
 * Reveal walks the tree to it.
 *
 * It is a list of its own rather than the folder table with a column added,
 * because the two answer different questions: the table is one folder's
 * contents and the breadcrumb above it says which, while every hit here can be
 * somewhere different.
 */
export function SearchHits({
  resources,
  admin,
  onOpen,
  onReveal,
}: {
  resources: Resource[];
  admin: boolean;
  onOpen: (resource: Resource) => void;
  /** Walks the tree to the folder the hit is in, with the hit still in view. */
  onReveal: (resource: Resource) => void;
}) {
  return (
    <ul className="divide-y overflow-hidden rounded-lg border bg-card" data-testid="search-hits">
      {resources.map((r) => (
        <li
          key={r.id}
          data-testid={`search-hit-${r.id}`}
          onClick={() => onOpen(r)}
          className="flex cursor-pointer items-center gap-3 px-4 py-2.5 hover:bg-muted/50"
        >
          <File className="size-4 shrink-0 text-muted-foreground" />
          <div className="min-w-0 flex-1">
            <span className="block truncate text-sm font-medium">{r.display_name}</span>
            <span className="block truncate text-xs text-muted-foreground">
              {markdownToPlainText(r.description)}
            </span>
          </div>
          {admin && <ScopeBadge scope={r.scope} scopeId={r.scope_id} />}
          <span
            className="hidden max-w-[16rem] shrink-0 truncate font-mono text-xs text-muted-foreground sm:block"
            title={r.path}
            data-testid={`search-hit-path-${r.id}`}
          >
            {r.path}
          </span>
          <Button
            variant="ghost"
            size="icon-xs"
            title={`Reveal in ${r.path}`}
            aria-label={`Reveal ${r.display_name} in ${r.path}`}
            onClick={(e) => {
              e.stopPropagation();
              onReveal(r);
            }}
          >
            <CornerDownRight />
          </Button>
        </li>
      ))}
    </ul>
  );
}
