import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { FLOW_TILE_TYPE, FlowTile } from "./FlowTile";
import { Tile } from "./Tile";
import { sampleGraph } from "@/pages/scripts/flow/testGraph";
import { tileFit, LEGIBLE_SCALE } from "@/pages/scripts/flow/FlowTileSvg";
import { layoutFlow } from "@/pages/scripts/flow/flowLayout";

// A tile is drawn after an asynchronous layout, which takes longer than the
// default second on a loaded machine. Loading the layout engine is paid once
// here rather than by the first test.
const LAYOUT_WAIT = { timeout: 4_000 };

beforeAll(async () => {
  await layoutFlow(sampleGraph());
}, 30_000);

afterEach(cleanup);

describe("FlowTile", () => {
  it("draws the Structure view fit to the tile and says it is drawn (#1972)", async () => {
    const onDrawn = vi.fn();
    render(<FlowTile content={JSON.stringify(sampleGraph())} onDrawn={onDrawn} />);
    const tile = await screen.findByTestId("flow-tile", {}, LAYOUT_WAIT);
    expect(tile).toHaveAttribute("data-view", "structure");
    expect(tile.querySelectorAll("[data-struct]")).toHaveLength(sampleGraph().structure.nodes.length);
    await waitFor(() => expect(onDrawn).toHaveBeenCalledWith(""), LAYOUT_WAIT);
  });

  it("draws the value graph from a graph that carries no structure", async () => {
    const onDrawn = vi.fn();
    const { structure: _, ...older } = sampleGraph();
    render(<FlowTile content={JSON.stringify(older)} onDrawn={onDrawn} />);
    const tile = await screen.findByTestId("flow-tile", {}, LAYOUT_WAIT);
    expect(tile.querySelectorAll("[data-node]")).toHaveLength(sampleGraph().nodes.length);
  });

  it("draws an empty diagram for a script with no platform calls, not an error", async () => {
    const onDrawn = vi.fn();
    const g = sampleGraph();
    render(
      <FlowTile
        content={JSON.stringify({ ...g, nodes: [], edges: [], groups: [], structure: { ...g.structure, nodes: [] } })}
        onDrawn={onDrawn}
      />,
    );
    expect(screen.getByTestId("flow-tile-empty")).toHaveTextContent("No platform calls");
    await waitFor(() => expect(onDrawn).toHaveBeenCalledWith(""));
  });

  it("says why when the graph cannot be read", () => {
    const onDrawn = vi.fn();
    render(<FlowTile content="not json" onDrawn={onDrawn} />);
    expect(onDrawn).toHaveBeenCalledWith("the flow graph could not be read");
  });

  it("is what the tile page draws for the flow content type", async () => {
    const onDrawn = vi.fn();
    render(
      <Tile
        data={{ contentType: FLOW_TILE_TYPE, name: "s", content: JSON.stringify(sampleGraph()), contentURL: "", serveFromURL: false }}
        dark={false}
        onDrawn={onDrawn}
      />,
    );
    expect(await screen.findByTestId("flow-tile", {}, LAYOUT_WAIT)).toBeInTheDocument();
  });
});

describe("tileFit", () => {
  const layout = (width: number, height: number) => ({ width, height, nodes: [], groups: [], edges: [] });
  it("draws a small graph at full size, centered, rather than enlarging it", () => {
    expect(tileFit(layout(100, 50), 400, 300)).toEqual({ scale: 1, x: 150, y: 125 });
  });
  it("shrinks a large graph to fit with padding", () => {
    const fit = tileFit(layout(2000, 600), 400, 300);
    expect(fit.scale).toBeCloseTo(376 / 2000);
    expect(fit.scale).toBeLessThan(LEGIBLE_SCALE);
  });
});
