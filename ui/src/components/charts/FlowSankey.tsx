import { useEffect, useMemo, useRef, useState } from "react";
import {
  sankey,
  sankeyLinkHorizontal,
  sankeyJustify,
  type SankeyNodeMinimal,
  type SankeyLinkMinimal,
} from "d3-sankey";
import type { FlowGraph, FlowNode } from "@/pages/audit/promql";
import { ChartSkeleton } from "./ChartSkeleton";

// FlowSankey renders a connection -> operation traffic-flow diagram so an
// operator can see, proportionally, where API gateway volume goes: which
// connections dominate and how each splits across its operations. It
// measures its container so the diagram fills the full width (no
// letterboxing), and sorts nodes within each column by volume (descending)
// so the heaviest connections/operations sit at the top. d3-sankey computes
// the layout; React renders the SVG.
interface FlowSankeyProps {
  graph: FlowGraph;
  isLoading: boolean;
  height?: number;
}

type SNode = FlowNode & SankeyNodeMinimal<FlowNode, { value: number }>;
type SLink = SankeyLinkMinimal<FlowNode, { value: number }> & { value: number };

const CONN_COLOR = "hsl(221, 83%, 60%)";
const OP_COLOR = "hsl(172, 66%, 50%)";

function useElementWidth<T extends HTMLElement>() {
  const ref = useRef<T>(null);
  const [width, setWidth] = useState(0);
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const ro = new ResizeObserver((entries) => setWidth(entries[0]?.contentRect.width ?? 0));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  return [ref, width] as const;
}

export function FlowSankey({ graph, isLoading, height = 320 }: FlowSankeyProps) {
  const [ref, width] = useElementWidth<HTMLDivElement>();

  const layout = useMemo(() => {
    if (width <= 0 || graph.nodes.length === 0 || graph.links.length === 0) return null;
    // d3-sankey mutates its input, so clone. nodeSort orders nodes within
    // each column by descending volume.
    const gen = sankey<FlowNode, { value: number }>()
      .nodeWidth(12)
      .nodePadding(10)
      .nodeAlign(sankeyJustify)
      .nodeSort((a, b) => (b.value ?? 0) - (a.value ?? 0))
      // Inset top/bottom so the first/last node labels (centered on the
      // node, ~6px tall each way) are not clipped by the SVG edge.
      .extent([
        [1, 8],
        [width - 1, height - 8],
      ]);
    return gen({
      nodes: graph.nodes.map((n) => ({ ...n })),
      links: graph.links.map((l) => ({ ...l })),
    });
  }, [graph, width, height]);

  const isEmpty = graph.nodes.length === 0 || graph.links.length === 0;
  const linkPath = sankeyLinkHorizontal<FlowNode, { value: number }>();

  // The ref div renders in EVERY state so the ResizeObserver always
  // attaches on mount (otherwise width stays 0 and the SVG never appears
  // when the component first mounts in the loading state).
  return (
    <div ref={ref} className="w-full">
      {isLoading ? (
        <ChartSkeleton height={height} />
      ) : isEmpty ? (
        <div
          className="flex items-center justify-center rounded-md border border-dashed text-sm text-muted-foreground"
          style={{ height }}
        >
          No traffic in this window.
        </div>
      ) : layout ? (
        <svg width={width} height={height} role="img" aria-label="Connection to operation traffic flow">
          <g fill="none">
            {(layout.links as SLink[]).map((link, i) => {
              const src = link.source as SNode;
              return (
                <path
                  key={i}
                  d={linkPath(link) ?? undefined}
                  stroke={src.kind === "connection" ? CONN_COLOR : OP_COLOR}
                  strokeOpacity={0.28}
                  strokeWidth={Math.max(1, link.width ?? 1)}
                >
                  <title>{`${(link.source as SNode).name} → ${(link.target as SNode).name}: ${link.value.toLocaleString()}`}</title>
                </path>
              );
            })}
          </g>
          {(layout.nodes as SNode[]).map((node, i) => {
            const x0 = node.x0 ?? 0;
            const x1 = node.x1 ?? 0;
            const y0 = node.y0 ?? 0;
            const y1 = node.y1 ?? 0;
            const isConn = node.kind === "connection";
            const labelRight = x0 < width / 2;
            return (
              <g key={i}>
                <rect
                  x={x0}
                  y={y0}
                  width={x1 - x0}
                  height={Math.max(1, y1 - y0)}
                  rx={2}
                  fill={isConn ? CONN_COLOR : OP_COLOR}
                >
                  <title>{`${node.name}: ${(node.value ?? 0).toLocaleString()}`}</title>
                </rect>
                <text
                  x={labelRight ? x1 + 6 : x0 - 6}
                  y={(y0 + y1) / 2}
                  textAnchor={labelRight ? "start" : "end"}
                  dominantBaseline="central"
                  fontSize={11}
                  fill="hsl(var(--foreground))"
                  className="font-mono"
                >
                  {node.name}
                </text>
              </g>
            );
          })}
        </svg>
      ) : (
        // Width not yet measured: hold the panel height to avoid layout shift.
        <div style={{ height }} />
      )}
    </div>
  );
}
