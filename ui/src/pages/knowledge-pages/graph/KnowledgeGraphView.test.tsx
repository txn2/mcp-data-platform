import { describe, it, expect, vi, beforeEach } from "vitest";
import { fireEvent, render, screen, within } from "@testing-library/react";
import type { KnowledgeGraphResponse } from "@/api/portal/hooks";
import { KnowledgeGraphView } from "./KnowledgeGraphView";
import { ApiError } from "@/api/portal/client";

// The graph's own states are driven by the endpoint's envelope, so the query is
// stubbed here. The states this file covers (an empty corpus, truncation, and
// the analysis readouts) are not reachable from the MSW seed; every interaction
// that IS reachable is covered end-to-end in
// e2e/interactive/knowledge-graph.spec.ts against MSW.
const graphResult = vi.hoisted(() => ({
  current: { data: undefined as KnowledgeGraphResponse | undefined, isLoading: false, isError: false },
}));

vi.mock("@/api/portal/hooks", () => ({
  useKnowledgeGraph: () => graphResult.current,
}));

// Selecting a catalog node resolves it against DataHub. That lookup is the
// subject of its own assertions below; everywhere else it just has to not
// require a live QueryClient.
const catalogResult = vi.hoisted(() => ({
  // error is optional: only the lookup's own assertions set one.
  current: { data: undefined, isLoading: false, isError: false } as {
    data: unknown;
    isLoading: boolean;
    isError: boolean;
    error?: unknown;
  },
}));

vi.mock("@/api/portal/datahub", () => ({
  useDataHubConnections: () => ({ data: [{ name: "acme", writable: false }] }),
  useCatalogEntity: () => catalogResult.current,
}));

function pageNode(id: string, label: string) {
  return { id: `mcp:knowledge_page:${id}`, type: "knowledge_page", label, exists: true, page: true };
}

function emptyGraph(): KnowledgeGraphResponse {
  return { nodes: [], edges: [], total_pages: 0, truncated: false };
}

function oneNodeGraph(overrides: Partial<KnowledgeGraphResponse> = {}): KnowledgeGraphResponse {
  return {
    nodes: [{ ...pageNode("kp1", "Fiscal Calendar"), tags: ["finance"] }],
    edges: [],
    total_pages: 1,
    truncated: false,
    ...overrides,
  };
}

/** twoTypeGraph is a page citing one catalog entity, for the type-filter path. */
function twoTypeGraph(): KnowledgeGraphResponse {
  const base = oneNodeGraph();
  return {
    ...base,
    nodes: [
      ...base.nodes,
      {
        id: "urn:li:dataset:(trino,sales.orders,PROD)",
        type: "datahub",
        label: "sales.orders",
        exists: true,
        page: false,
      },
    ],
    edges: [
      {
        source: "mcp:knowledge_page:kp1",
        target: "urn:li:dataset:(trino,sales.orders,PROD)",
        type: "datahub",
        ref_source: "inline",
      },
    ],
  };
}

/**
 * bridgeGraph is two clusters of pages joined by one catalog entity — the shape
 * the whole view exists to surface. The dataset is the only bridge.
 */
function bridgeGraph(): KnowledgeGraphResponse {
  const bridge = {
    id: "urn:li:dataset:(trino,sales.orders,PROD)",
    type: "datahub",
    label: "sales.orders",
    exists: true,
    page: false,
  };
  // Each side is a chain, not a clique, so depth 2 from the bridge cannot reach
  // the far end. A test that asserts "the view is scoped" needs a corpus the
  // default depth genuinely does not cover.
  const left = ["l1", "l2", "l3", "l4"].map((id) => pageNode(id, `Left ${id}`));
  const right = ["r1", "r2", "r3", "r4"].map((id) => pageNode(id, `Right ${id}`));
  const ref = (source: string, target: string) => ({
    source,
    target,
    type: "knowledge_page",
    ref_source: "manual",
  });
  return {
    nodes: [...left, bridge, ...right],
    edges: [
      ref(left[0]!.id, left[1]!.id),
      ref(left[1]!.id, left[2]!.id),
      ref(left[2]!.id, left[3]!.id),
      ref(right[0]!.id, right[1]!.id),
      ref(right[1]!.id, right[2]!.id),
      ref(right[2]!.id, right[3]!.id),
      { ...ref(left[0]!.id, bridge.id), type: "datahub" },
      { ...ref(right[0]!.id, bridge.id), type: "datahub" },
    ],
    total_pages: 8,
    truncated: false,
  };
}

function renderGraph(props: Partial<Parameters<typeof KnowledgeGraphView>[0]> = {}) {
  return render(
    <KnowledgeGraphView tag="" query="" onOpenPage={vi.fn()} onNavigate={vi.fn()} {...props} />,
  );
}

/** typeChip finds a node-type filter chip, which shares its label with the node
 * marks (both are buttons), so it must be scoped to the filter group. */
function typeChip(name: RegExp) {
  return within(screen.getByRole("group", { name: "Node types" })).getByRole("button", { name });
}

describe("KnowledgeGraphView", () => {
  beforeEach(() => {
    graphResult.current = { data: undefined, isLoading: false, isError: false };
    catalogResult.current = { data: undefined, isLoading: false, isError: false };
  });

  it("explains the empty corpus instead of drawing an empty canvas", () => {
    graphResult.current = { data: emptyGraph(), isLoading: false, isError: false };
    renderGraph();

    expect(screen.getByText("No knowledge pages to graph yet.")).toBeInTheDocument();
    expect(screen.getByText(/Cite an entity on a page to give it an edge/)).toBeInTheDocument();
    expect(screen.queryByRole("application", { name: "Knowledge graph" })).not.toBeInTheDocument();
  });

  it("names the active tag when the filter is what emptied the graph", () => {
    graphResult.current = { data: emptyGraph(), isLoading: false, isError: false };
    renderGraph({ tag: "governance" });

    expect(screen.getByText('No pages tagged "governance" to graph.')).toBeInTheDocument();
  });

  it("shows the server's truncation notice rather than capping silently", () => {
    const notice = "Showing the 100 most recently updated of 340 pages. Filter by tag to see the rest.";
    graphResult.current = {
      data: oneNodeGraph({ truncated: true, notice, total_pages: 340 }),
      isLoading: false,
      isError: false,
    };
    renderGraph();

    expect(screen.getByText(notice)).toBeInTheDocument();
  });

  it("shows no notice when the graph is the whole corpus", () => {
    graphResult.current = { data: oneNodeGraph(), isLoading: false, isError: false };
    renderGraph();

    expect(screen.queryByText(/most recently updated of/)).not.toBeInTheDocument();
    expect(screen.getByRole("application", { name: "Knowledge graph" })).toBeInTheDocument();
  });

  it("reports a failed load", () => {
    graphResult.current = { data: undefined, isLoading: false, isError: true };
    renderGraph();

    expect(screen.getByText(/Failed to load the knowledge graph/)).toBeInTheDocument();
  });

  it("opens on the corpus's strongest bridge rather than the whole hairball", () => {
    // The dataset is the only node any shortest path between the two chains runs
    // through, so it is where exploration starts.
    graphResult.current = { data: bridgeGraph(), isLoading: false, isError: false };
    renderGraph();

    expect(screen.getByText(/Around/)).toHaveTextContent("Around sales.orders");
    // Depth 2 reaches each chain's first two pages and stops: 1 bridge + 4 of
    // the 8 pages. The far ends are NOT drawn, which is what "scoped" means.
    expect(screen.getByText(/5 of 9 nodes/)).toBeInTheDocument();
    expect(screen.getByLabelText("Page: Left l1")).toBeInTheDocument();
    expect(screen.queryByLabelText("Page: Left l4")).not.toBeInTheDocument();
  });

  it("clears a traced path so focus and depth keep working afterwards", () => {
    // A path force-adds its nodes to the visible set. Left set, it pins the
    // whole chain on screen and every later navigation looks broken.
    graphResult.current = { data: bridgeGraph(), isLoading: false, isError: false };
    renderGraph();
    fireEvent.click(screen.getByRole("button", { name: "Whole corpus" }));

    fireEvent.click(screen.getByLabelText("Page: Left l4"));
    fireEvent.click(within(screen.getByRole("complementary")).getByRole("button", { name: /Path from/ }));
    fireEvent.click(screen.getByLabelText("Page: Right r4"));
    expect(within(screen.getByRole("complementary")).getByText(/hops apart/)).toBeInTheDocument();

    fireEvent.click(within(screen.getByRole("complementary")).getByRole("button", { name: /Focus/ }));

    // Focusing on r4 at depth 2 must not still be dragging the whole traced
    // chain into view.
    expect(screen.getByText(/^Around /)).toHaveTextContent("Around Right r4");
    expect(screen.queryByLabelText("Page: Left l4")).not.toBeInTheDocument();
  });

  it("stops inspecting a node the type filter removed", () => {
    graphResult.current = { data: twoTypeGraph(), isLoading: false, isError: false };
    renderGraph();
    fireEvent.click(screen.getByLabelText("Catalog: sales.orders"));
    expect(within(screen.getByRole("complementary")).getByText("sales.orders")).toBeInTheDocument();

    fireEvent.click(typeChip(/^Catalog/));

    // The pane must not keep offering Focus and Path on something that is no
    // longer in the graph.
    expect(within(screen.getByRole("complementary")).queryByText("sales.orders")).not.toBeInTheDocument();
  });

  it("counts search matches across the corpus, not just the drawn slice", () => {
    graphResult.current = { data: bridgeGraph(), isLoading: false, isError: false };
    renderGraph({ query: "Left l4" });

    // Left l4 is four hops from the focus, so it is not drawn — but it IS in the
    // corpus, and reporting "0 matching" would read as "nothing matches".
    expect(screen.getByText(/1 matching "Left l4"/)).toBeInTheDocument();
    expect(screen.getByText(/1 outside this view/)).toBeInTheDocument();
  });

  it("reports the bridge score of the selected node", () => {
    graphResult.current = { data: bridgeGraph(), isLoading: false, isError: false };
    renderGraph();

    const inspector = screen.getByRole("complementary");
    expect(within(inspector).getByText("sales.orders")).toBeInTheDocument();
    expect(within(inspector).getByText("Bridges")).toBeInTheDocument();
    // 4 left pages x 4 right pages = 16 cross-chain pairs, every one of which
    // has to pass through the dataset.
    expect(within(inspector).getByText(/16 paths/)).toBeInTheDocument();
  });

  it("reports the clusters it detected", () => {
    graphResult.current = { data: bridgeGraph(), isLoading: false, isError: false };
    renderGraph();

    expect(screen.getByText(/clusters \(modularity/)).toBeInTheDocument();
  });

  it("selects a node in place instead of navigating away", () => {
    const onOpenPage = vi.fn();
    graphResult.current = { data: bridgeGraph(), isLoading: false, isError: false };
    renderGraph({ onOpenPage });

    fireEvent.click(screen.getByRole("button", { name: "Page: Left l1" }));

    expect(onOpenPage).not.toHaveBeenCalled();
    expect(within(screen.getByRole("complementary")).getByText("Left l1")).toBeInTheDocument();
  });

  it("opens the page only on the inspector's explicit action", () => {
    const onOpenPage = vi.fn();
    graphResult.current = { data: bridgeGraph(), isLoading: false, isError: false };
    renderGraph({ onOpenPage });

    fireEvent.click(screen.getByRole("button", { name: "Page: Left l1" }));
    fireEvent.click(within(screen.getByRole("complementary")).getByRole("button", { name: /Open/ }));

    expect(onOpenPage).toHaveBeenCalledWith("l1");
  });

  it("traces the shortest path between two nodes", () => {
    graphResult.current = { data: bridgeGraph(), isLoading: false, isError: false };
    renderGraph();

    const inspector = () => screen.getByRole("complementary");
    fireEvent.click(screen.getByRole("button", { name: "Page: Left l2" }));
    fireEvent.click(within(inspector()).getByRole("button", { name: /Path from/ }));
    expect(within(inspector()).getByText(/Click another node/)).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Page: Right r1" }));

    // l2 -> l1 -> sales.orders -> r1 is three hops.
    expect(within(inspector()).getByText("3 hops apart")).toBeInTheDocument();
  });

  it("says so when two nodes are not connected at all", () => {
    graphResult.current = {
      data: {
        nodes: [pageNode("a", "Alone A"), pageNode("b", "Alone B")],
        edges: [],
        total_pages: 2,
        truncated: false,
      },
      isLoading: false,
      isError: false,
    };
    renderGraph();

    // Two unconnected nodes are only on screen together in corpus mode: the
    // focus view shows one node's neighbourhood, and they are in different ones.
    fireEvent.click(screen.getByRole("button", { name: "Whole corpus" }));

    const inspector = () => screen.getByRole("complementary");
    fireEvent.click(screen.getByRole("button", { name: "Page: Alone A" }));
    fireEvent.click(within(inspector()).getByRole("button", { name: /Path from/ }));
    fireEvent.click(screen.getByRole("button", { name: "Page: Alone B" }));

    expect(within(inspector()).getByText(/not connected by any chain of references/)).toBeInTheDocument();
  });

  it("widens the neighbourhood one hop at a time", () => {
    graphResult.current = { data: bridgeGraph(), isLoading: false, isError: false };
    renderGraph();

    fireEvent.click(screen.getByRole("button", { name: "1" }));
    expect(screen.getByText(/3 of 9 nodes/)).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "3" }));
    // Three hops from the bridge reaches each chain's third page; the far ends
    // are four hops out and stay off screen.
    expect(screen.getByText(/7 of 9 nodes/)).toBeInTheDocument();
  });

  it("drops back to the whole corpus on demand", () => {
    graphResult.current = { data: bridgeGraph(), isLoading: false, isError: false };
    renderGraph();

    fireEvent.click(screen.getByRole("button", { name: "Whole corpus" }));

    expect(screen.getByText(/9 nodes, 8 references/)).toBeInTheDocument();
    expect(screen.queryByText(/Around/)).not.toBeInTheDocument();
  });

  it("wires a type chip through to the drawn graph", () => {
    graphResult.current = { data: twoTypeGraph(), isLoading: false, isError: false };
    renderGraph();
    expect(screen.getByLabelText("Catalog: sales.orders")).toBeInTheDocument();

    fireEvent.click(typeChip(/^Catalog/));

    // The catalog node is gone, and hiding it did not take the page with it —
    // pages are the corpus the graph is of and are never hidden.
    expect(screen.queryByLabelText("Catalog: sales.orders")).not.toBeInTheDocument();
    expect(screen.getByLabelText("Page: Fiscal Calendar")).toBeInTheDocument();
  });
});

describe("KnowledgeGraphView catalog nodes", () => {
  const CATALOG_URN = "urn:li:dataset:(trino,sales.orders,PROD)";

  beforeEach(() => {
    graphResult.current = { data: twoTypeGraph(), isLoading: false, isError: false };
    catalogResult.current = { data: undefined, isLoading: false, isError: false };
  });

  function selectCatalogNode() {
    renderGraph();
    fireEvent.click(screen.getByLabelText("Catalog: sales.orders"));
    return screen.getByRole("complementary");
  }

  it("shows the URN, which is the only identifier the label is derived from", () => {
    // The label is the dataset segment of the URN, so it is neither the entity's
    // catalog name nor a string the catalog can be searched for. Without the URN
    // on screen there is nothing the reader can act on.
    expect(within(selectCatalogNode()).getByText(CATALOG_URN)).toBeInTheDocument();
  });

  it("offers an Open action that deep-links the catalog entity", () => {
    const onNavigate = vi.fn();
    graphResult.current = { data: twoTypeGraph(), isLoading: false, isError: false };
    renderGraph({ onNavigate });
    fireEvent.click(screen.getByLabelText("Catalog: sales.orders"));

    fireEvent.click(within(screen.getByRole("complementary")).getByRole("button", { name: /Open/ }));

    expect(onNavigate).toHaveBeenCalledWith(
      `/knowledge/catalog?urn=${encodeURIComponent(CATALOG_URN)}#tables`,
    );
  });

  it("says so when a page cites a dataset the catalog does not have", () => {
    // The catalog reports a URN it has never ingested, and the read answers 404
    // (#1610); a record carrying only its own URN is a dataset the catalog
    // holds and nobody has documented, not a missing one.
    catalogResult.current = {
      data: undefined,
      isLoading: false,
      isError: true,
      error: new ApiError(404, "datahub holds no entity"),
    };
    const pane = selectCatalogNode();
    expect(within(pane).getByText(/Not found in/)).toBeInTheDocument();
    expect(within(pane).getByText(/the catalog does not have it/)).toBeInTheDocument();
  });

  it("reports a catalog it could not reach as a failure rather than an absence", () => {
    catalogResult.current = {
      data: undefined,
      isLoading: false,
      isError: true,
      error: new ApiError(502, "entity read failed"),
    };
    const pane = selectCatalogNode();
    expect(within(pane).getByText(/Could not reach the acme catalog/)).toBeInTheDocument();
    expect(within(pane).queryByText(/Not found in/)).not.toBeInTheDocument();
  });

  it("shows what the catalog holds when the entity resolves", () => {
    catalogResult.current = {
      data: {
        urn: CATALOG_URN,
        context: { urn: CATALOG_URN, description: "One row per store per day.", tags: ["PII"] },
      },
      isLoading: false,
      isError: false,
    };
    const pane = selectCatalogNode();
    expect(within(pane).getByText("One row per store per day.")).toBeInTheDocument();
    expect(within(pane).getByText("PII")).toBeInTheDocument();
    expect(within(pane).queryByText(/Not found in/)).not.toBeInTheDocument();
  });

  it("names the connection it looked in rather than claiming the whole platform", () => {
    // On a deployment with several catalogs, "not in the catalog" would be a
    // guess about the ones it did not query.
    catalogResult.current = {
      data: undefined,
      isLoading: false,
      isError: true,
      error: new ApiError(404, "datahub holds no entity"),
    };
    expect(within(selectCatalogNode()).getByText("acme")).toBeInTheDocument();
  });

  it("shows a dataset the catalog holds and nobody has documented as held", () => {
    catalogResult.current = {
      data: { urn: CATALOG_URN, context: { urn: CATALOG_URN } },
      isLoading: false,
      isError: false,
    };
    const pane = selectCatalogNode();
    expect(
      within(pane).getByText(/holds this dataset with no description, owners or tags recorded/),
    ).toBeInTheDocument();
    expect(within(pane).queryByText(/Not found in/)).not.toBeInTheDocument();
  });
});
