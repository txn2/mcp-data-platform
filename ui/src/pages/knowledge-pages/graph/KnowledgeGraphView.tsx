import { Share2 } from "lucide-react";
import { useKnowledgeGraph, type KnowledgeGraphResponse } from "@/api/portal/hooks";
import { EmptyState } from "@/components/patterns/EmptyState";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { GraphWorkspace } from "./GraphWorkspace";
import { useElementSize } from "./useElementSize";

const FALLBACK_WIDTH = 900;

interface KnowledgeGraphViewProps {
  /** The active tag filter, applied server-side (it narrows the corpus). */
  tag: string;
  /** The active search text; it focuses matching nodes rather than refetching. */
  query: string;
  onOpenPage: (id: string) => void;
  onNavigate?: (path: string) => void;
}

/**
 * KnowledgeGraphView is the graph layout of the knowledge corpus (#1162). It
 * loads the corpus and measures the space; GraphWorkspace analyses and draws it.
 *
 * The measured container is the outermost element in EVERY state, including
 * loading. Returning early above it would leave the size callback with nothing
 * to observe, and the canvas would keep its fallback width for the life of the
 * mount.
 */
export function KnowledgeGraphView({ tag, query, onOpenPage, onNavigate }: KnowledgeGraphViewProps) {
  const { data, isLoading, isError } = useKnowledgeGraph({ tag: tag || undefined });
  const [containerRef, width] = useElementSize<HTMLDivElement>(FALLBACK_WIDTH);

  return (
    <div ref={containerRef}>
      {renderState({ data, isLoading, isError, tag }) ?? (
        <GraphWorkspace
          data={data as KnowledgeGraphResponse}
          width={width}
          query={query}
          onOpenPage={onOpenPage}
          onNavigate={onNavigate}
        />
      )}
    </div>
  );
}

/** renderState returns the message for a non-ready state, or null when the
 * corpus is loaded and has something to draw. */
function renderState({
  data,
  isLoading,
  isError,
  tag,
}: {
  data: KnowledgeGraphResponse | undefined;
  isLoading: boolean;
  isError: boolean;
  tag: string;
}) {
  if (isError) {
    return (
      <Alert variant="destructive">
        <AlertDescription>
          Failed to load the knowledge graph. Please try again.
        </AlertDescription>
      </Alert>
    );
  }
  if (isLoading || !data) {
    return <p className="text-sm text-muted-foreground">Loading graph...</p>;
  }
  if (data.nodes.length === 0) {
    return (
      <EmptyState icon={Share2}>
        {tag ? `No pages tagged "${tag}" to graph.` : "No knowledge pages to graph yet."}
        <p className="mt-2 text-xs">
          The graph draws pages and the entities they reference. Cite an entity on a page to give it
          an edge.
        </p>
      </EmptyState>
    );
  }
  return null;
}
