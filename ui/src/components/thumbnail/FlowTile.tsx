import { useEffect, useState } from "react";
import type { ScriptFlow } from "@/api/portal/hooks/scriptFlow";
import { THUMB_HEIGHT, THUMB_WIDTH } from "@/lib/thumbnailSupport";
import { layoutFlow, type FlowLayout } from "@/pages/scripts/flow/flowLayout";
import { FlowTileSvg } from "@/pages/scripts/flow/FlowTileSvg";

/**
 * FLOW_TILE_TYPE is the content type a script's flow graph reaches the tile
 * page as (#1909), the same constant the tile worker sends
 * (internal/platform/thumbworker.FlowTileType). It is no file's type.
 */
export const FLOW_TILE_TYPE = "application/vnd.mcp-data-platform.flow-graph";

/**
 * FlowTile is a script's tile: its flow diagram, laid out as the Flow tab lays
 * it out and fit to the tile. A script with no platform calls is an empty
 * diagram, drawn and stated, not an error.
 */
export function FlowTile({ content, onDrawn }: { content: string; onDrawn: (reason: string) => void }) {
  const [layout, setLayout] = useState<FlowLayout | null>(null);
  const [empty, setEmpty] = useState(false);

  useEffect(() => {
    let graph: ScriptFlow;
    try {
      graph = JSON.parse(content) as ScriptFlow;
    } catch {
      onDrawn("the flow graph could not be read");
      return;
    }
    if (graph.nodes.length === 0) {
      setEmpty(true);
      return;
    }
    let live = true;
    layoutFlow(graph).then(
      (l) => live && setLayout(l),
      () => live && onDrawn("the flow graph could not be laid out"),
    );
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
  return <FlowTileSvg layout={layout} width={THUMB_WIDTH} height={THUMB_HEIGHT} />;
}
