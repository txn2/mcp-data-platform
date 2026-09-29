import { useEffect, useState } from "react";
import type { ScriptFlow } from "@/api/portal/hooks/scriptFlow";
import { THUMB_HEIGHT, THUMB_WIDTH } from "@/lib/thumbnailSupport";
import { layoutFlow, type FlowLayout } from "@/pages/scripts/flow/flowLayout";
import { FlowTileSvg, StructureTileSvg } from "@/pages/scripts/flow/FlowTileSvg";
import { layoutStructure, type StructureLayout } from "@/pages/scripts/flow/structureLayout";
import { defaultFolded, fold } from "@/pages/scripts/flow/structureModel";

/**
 * FLOW_TILE_TYPE is the content type a script's flow graph reaches the tile
 * page as (#1909), the same constant the tile worker sends
 * (internal/platform/thumbworker.FlowTileType). It is no file's type.
 */
export const FLOW_TILE_TYPE = "application/vnd.mcp-data-platform.flow-graph";

/**
 * FlowTile is a script's tile: its flow diagram, laid out as the Flow tab lays
 * it out and fit to the tile. It draws the Structure view the tab opens on
 * (#1972), and the value graph for a graph that carries no structure. A
 * script with no platform calls is an empty diagram, drawn and stated, not an
 * error.
 */
export function FlowTile({ content, onDrawn }: { content: string; onDrawn: (reason: string) => void }) {
  const [layout, setLayout] = useState<
    { kind: "calls"; layout: FlowLayout } | { kind: "structure"; layout: StructureLayout; graph: ScriptFlow } | null
  >(null);
  const [empty, setEmpty] = useState(false);

  useEffect(() => {
    let graph: ScriptFlow;
    try {
      graph = JSON.parse(content) as ScriptFlow;
    } catch {
      onDrawn("the flow graph could not be read");
      return;
    }
    const structure = graph.structure;
    if (graph.nodes.length === 0 && (!structure || structure.nodes.length <= 2)) {
      setEmpty(true);
      return;
    }
    let live = true;
    const failed = () => live && onDrawn("the flow graph could not be laid out");
    if (structure && structure.nodes.length > 0) {
      layoutStructure(fold(structure, defaultFolded(structure)), graph).then(
        (l) => live && setLayout({ kind: "structure", layout: l, graph }),
        failed,
      );
    } else {
      layoutFlow(graph).then((l) => live && setLayout({ kind: "calls", layout: l }), failed);
    }
    return () => {
      live = false;
    };
  }, [content, onDrawn]);

  useEffect(() => {
    if (!layout && !empty) return;
    requestAnimationFrame(() => requestAnimationFrame(() => onDrawn("")));
  }, [layout, empty, onDrawn]);

  if (empty) {
    return (
      <div
        data-testid="flow-tile-empty"
        style={{
          width: THUMB_WIDTH,
          height: THUMB_HEIGHT,
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          background: "hsl(var(--background))",
          color: "hsl(var(--muted-foreground))",
          font: "13px ui-sans-serif, system-ui, sans-serif",
        }}
      >
        No platform calls
      </div>
    );
  }
  if (!layout) return null;
  if (layout.kind === "structure") {
    return <StructureTileSvg layout={layout.layout} graph={layout.graph} width={THUMB_WIDTH} height={THUMB_HEIGHT} />;
  }
  return <FlowTileSvg layout={layout.layout} width={THUMB_WIDTH} height={THUMB_HEIGHT} />;
}
