import type {
  KnowledgeGraphEdge,
  KnowledgeGraphNode,
  KnowledgeGraphResponse,
} from "@/api/portal/hooks";
import { extractRefUrns, parseRef } from "@/lib/entityRefs";
import { mockAssets } from "./assets";
import { mockKnowledgePages } from "./knowledgePages";

// The mock knowledge graph is DERIVED from the seeded page bodies rather than
// hand-authored, so it stays faithful to what the real endpoint returns: the
// backend builds the graph from the references it scanned out of those same
// bodies. Hand-maintaining a second copy would drift, and a graph whose edges do
// not correspond to the pages' actual citations would test nothing.

const PAGE_LIMIT = 100;

/** pageNodeID is the reference URN a knowledge page is keyed by. */
function pageNodeID(id: string): string {
  return `mcp:knowledge_page:${id}`;
}

/** entityNode builds a node for a referenced entity, resolving page titles. */
function entityNode(urn: string): KnowledgeGraphNode | null {
  const parsed = parseRef(urn);
  if (!parsed) return null;
  if (parsed.type === "knowledge_page") {
    const target = mockKnowledgePages.find((p) => p.id === parsed.id && !p.deleted_at);
    return {
      id: urn,
      type: "knowledge_page",
      label: target?.title ?? parsed.id,
      exists: !!target,
      page: false,
    };
  }
  if (parsed.type === "asset") {
    const asset = mockAssets.find((a) => a.id === parsed.id);
    return {
      id: urn,
      type: "asset",
      label: asset?.name ?? parsed.id,
      exists: !!asset,
      page: false,
    };
  }
  return { id: urn, type: parsed.type, label: parsed.fallbackLabel, exists: true, page: false };
}

/**
 * mockKnowledgeGraph returns the corpus graph for the seeded pages, honoring the
 * same tag and limit parameters the real endpoint takes and reporting truncation
 * the same way (an explicit notice, never a silent cut).
 */
export function mockKnowledgeGraph(tag: string, limit: number): KnowledgeGraphResponse {
  const matching = mockKnowledgePages.filter(
    (p) => !p.deleted_at && (!tag || p.tags.includes(tag)),
  );
  const window = matching.slice(0, limit > 0 ? limit : PAGE_LIMIT);

  const nodes = new Map<string, KnowledgeGraphNode>();
  const edges: KnowledgeGraphEdge[] = [];
  for (const p of window) {
    nodes.set(pageNodeID(p.id), {
      id: pageNodeID(p.id),
      type: "knowledge_page",
      label: p.title,
      exists: true,
      page: true,
      tags: p.tags,
      updated_at: p.updated_at,
    });
  }
  for (const p of window) {
    for (const urn of extractRefUrns(p.body)) {
      if (urn === pageNodeID(p.id)) continue; // a self-citation is not an edge
      const node = entityNode(urn);
      if (!node) continue;
      if (!nodes.has(urn)) nodes.set(urn, node);
      edges.push({ source: pageNodeID(p.id), target: urn, type: node.type, ref_source: "inline" });
    }
  }

  const truncated = matching.length > window.length;
  return {
    nodes: [...nodes.values()],
    edges,
    total_pages: matching.length,
    truncated,
    notice: truncated
      ? `Showing the ${window.length} most recently updated of ${matching.length} pages. Filter by tag to see the rest.`
      : undefined,
  };
}
