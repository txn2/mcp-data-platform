import { useCallback, useEffect, useRef, useState, type ComponentType } from "react";
import {
  ArrowLeftRight,
  Bell,
  CircleDot,
  CornerDownRight,
  Database,
  FileDown,
  History,
  Maximize2,
  Minus,
  PencilLine,
  Plus,
  RefreshCw,
  Save,
  Send,
  Table2,
  Wrench,
  type LucideProps,
} from "lucide-react";
import type { FlowNode } from "@/api/portal/hooks/scriptFlow";
import { Button } from "@/components/ui/button";
import type { FlowLayout, PlacedGroup, PlacedNode } from "./flowLayout";
import {
  FONT_BODY,
  FONT_CHIP,
  FONT_MONO,
  FONT_TITLE,
  ROLE_COLOR,
  ROLE_LABEL,
  SELECT_COLOR,
  cardText,
  fitText,
  textWidth,
  type Lit,
  type Selection,
} from "./flowModel";

// FlowCanvas draws a laid-out flow graph (#1906) on a dot-grid canvas the
// reader pans by dragging and zooms with the wheel or the buttons. It opens at
// a readable zoom anchored top-left rather than shrunk to fit: a diagram too
// small to read is not an overview, and Fit is one click away.

const KIND_ICON: Record<string, ComponentType<LucideProps>> = {
  query: Database,
  api: ArrowLeftRight,
  tool: Wrench,
  write: PencilLine,
  export: FileDown,
  table: Table2,
  publish_data: RefreshCw,
  save_state: Save,
  notify: Bell,
  publish: Send,
  result: CornerDownRight,
  state: History,
};

const MIN_ZOOM = 0.2;
const MAX_ZOOM = 2.5;
const OPEN_ZOOM = 0.9;
const MIN_HEIGHT = 420;
const MAX_HEIGHT = 720;

interface View {
  x: number;
  y: number;
  k: number;
}

interface Props {
  layout: FlowLayout;
  selection: Selection;
  lit: Lit;
  onSelect: (s: Selection) => void;
  onOpen: (n: FlowNode) => void;
}

export function FlowCanvas({ layout, selection, lit, onSelect, onOpen }: Props) {
  const hostRef = useRef<HTMLDivElement>(null);
  const [view, setView] = useState<View>({ x: 8, y: 8, k: OPEN_ZOOM });
  const drag = useRef<{ x: number; y: number; moved: boolean } | null>(null);
  const height = Math.round(
    Math.min(MAX_HEIGHT, Math.max(MIN_HEIGHT, layout.height * OPEN_ZOOM + 48)),
  );

  const zoomAt = useCallback((factor: number, cx: number, cy: number) => {
    setView((v) => {
      const k = Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, v.k * factor));
      return { k, x: cx - ((cx - v.x) * k) / v.k, y: cy - ((cy - v.y) * k) / v.k };
    });
  }, []);

  // The wheel listener is attached by hand because React's is passive, and a
  // passive listener cannot stop the page from scrolling under the zoom.
  useEffect(() => {
    const el = hostRef.current;
    if (!el) return;
    const onWheel = (e: WheelEvent) => {
      e.preventDefault();
      const r = el.getBoundingClientRect();
      zoomAt(Math.exp(-e.deltaY * 0.0015), e.clientX - r.left, e.clientY - r.top);
    };
    el.addEventListener("wheel", onWheel, { passive: false });
    return () => el.removeEventListener("wheel", onWheel);
  }, [zoomAt]);

  const fit = () => {
    const el = hostRef.current;
    const w = el?.clientWidth || 800;
    const k = Math.min(1, w / Math.max(1, layout.width), height / Math.max(1, layout.height)) * 0.98;
    setView({
      k,
      x: Math.max(0, (w - layout.width * k) / 2),
      y: Math.max(8, (height - layout.height * k) / 2),
    });
  };
  const zoomCentered = (f: number) => {
    const el = hostRef.current;
    zoomAt(f, (el?.clientWidth || 800) / 2, height / 2);
  };

  const groups = [...layout.groups].sort((a, b) => depth(a) - depth(b));
  const selectedGroup = selection?.kind === "group" ? selection.id : null;

  return (
    <div
      ref={hostRef}
      data-testid="flow-canvas"
      className="relative cursor-grab touch-none overflow-hidden rounded-md border select-none active:cursor-grabbing"
      style={{
        height,
        backgroundColor: "hsl(var(--muted) / 0.35)",
        backgroundImage: "radial-gradient(circle, hsl(var(--border)) 1px, transparent 1.2px)",
        backgroundSize: "22px 22px",
      }}
      onPointerDown={(e) => {
        if (e.button > 0) return;
        drag.current = { x: e.clientX - view.x, y: e.clientY - view.y, moved: false };
      }}
      onPointerMove={(e) => {
        const d = drag.current;
        if (!d) return;
        if (!d.moved && Math.hypot(e.clientX - view.x - d.x, e.clientY - view.y - d.y) < 3) return;
        d.moved = true;
        setView((v) => ({ ...v, x: e.clientX - d.x, y: e.clientY - d.y }));
      }}
      onPointerUp={() => {
        const d = drag.current;
        drag.current = null;
        if (d && !d.moved) onSelect(null);
      }}
      onPointerLeave={() => {
        drag.current = null;
      }}
    >
      <svg className="absolute inset-0 h-full w-full" role="group" aria-label="Flow diagram">
        <defs>
          <Arrow id="flow-arrow" color="hsl(var(--muted-foreground))" />
          <Arrow id="flow-arrow-lit" color={SELECT_COLOR} />
        </defs>
        <g transform={`translate(${view.x},${view.y}) scale(${view.k})`}>
          {groups.map((g) => (
            <GroupBox
              key={g.group.id}
              placed={g}
              selected={selectedGroup === g.group.id}
              onSelect={() => onSelect({ kind: "group", id: g.group.id })}
            />
          ))}
          {layout.edges.map((e) => {
            const hot = lit.edges.has(e.edge);
            const marker = `url(#${hot ? "flow-arrow-lit" : "flow-arrow"})`;
            return (
              <path
                key={e.key}
                d={e.path}
                fill="none"
                stroke={hot ? SELECT_COLOR : "hsl(var(--muted-foreground))"}
                strokeOpacity={hot ? 1 : 0.7}
                strokeWidth={hot ? 2.2 : 1.4}
                strokeDasharray={e.edge.kind === "state" ? "6 4" : undefined}
                markerStart={e.reversed ? marker : undefined}
                markerEnd={e.reversed ? undefined : marker}
                data-edge={`${e.edge.from}->${e.edge.to}`}
              >
                {e.edge.via.length > 0 && <title>{`reshaped by ${e.edge.via.join(", ")}`}</title>}
              </path>
            );
          })}
          {layout.nodes.map((p) => (
            <Card
              key={p.node.id}
              placed={p}
              selected={lit.nodes.has(p.node.id)}
              dimmed={lit.dimOthers && !lit.nodes.has(p.node.id)}
              onSelect={() => onSelect({ kind: "node", id: p.node.id })}
              onOpen={() => onOpen(p.node)}
            />
          ))}
        </g>
      </svg>
      <Legend />
      <div className="absolute right-3 bottom-3 flex gap-1" onPointerDown={(e) => e.stopPropagation()}>
        <Button type="button" variant="outline" size="icon-sm" aria-label="Zoom in" onClick={() => zoomCentered(1.2)}>
          <Plus />
        </Button>
        <Button type="button" variant="outline" size="icon-sm" aria-label="Zoom out" onClick={() => zoomCentered(1 / 1.2)}>
          <Minus />
        </Button>
        <Button type="button" variant="outline" size="icon-sm" aria-label="Fit the whole diagram" onClick={fit}>
          <Maximize2 />
        </Button>
      </div>
    </div>
  );
}

function depth(g: PlacedGroup): number {
  return g.group.id.split("/").length;
}

function Arrow({ id, color }: { id: string; color: string }) {
  return (
    <marker id={id} viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
      <path d="M0,0 L10,5 L0,10 z" fill={color} />
    </marker>
  );
}

function Legend() {
  return (
    <div className="absolute bottom-3 left-3 flex flex-wrap gap-3 rounded-md border bg-card px-2.5 py-1.5 text-xs text-muted-foreground">
      {(Object.keys(ROLE_COLOR) as Array<keyof typeof ROLE_COLOR>).map((r) => (
        <span key={r} className="inline-flex items-center gap-1.5">
          <i className="inline-block size-2.5 rounded-sm" style={{ background: ROLE_COLOR[r] }} />
          {ROLE_LABEL[r]}
        </span>
      ))}
      <span>dashed = known only when it runs</span>
    </div>
  );
}

function GroupBox({
  placed,
  selected,
  onSelect,
}: {
  placed: PlacedGroup;
  selected: boolean;
  onSelect: () => void;
}) {
  const { group, x, y, width, height } = placed;
  const calls =
    group.called_from.length > 1
      ? `called from lines ${group.called_from.join(", ")}`
      : `line ${group.called_from[0] ?? group.def_line}`;
  return (
    <g
      role="button"
      tabIndex={0}
      aria-label={`Function ${group.label}`}
      className="cursor-pointer outline-none"
      onPointerUp={(e) => {
        e.stopPropagation();
        onSelect();
      }}
      onPointerDown={(e) => e.stopPropagation()}
      onKeyDown={(e) => e.key === "Enter" && onSelect()}
    >
      <rect
        x={x}
        y={y}
        width={width}
        height={height}
        rx={12}
        fill="hsl(var(--muted) / 0.6)"
        stroke={selected ? SELECT_COLOR : "hsl(var(--border))"}
        strokeWidth={selected ? 2 : 1}
      />
      <text x={x + 14} y={y + 22} fontSize={13} fontWeight={650} fill="hsl(var(--foreground))" className="font-mono">
        {fitText(group.label, width - 150, FONT_TITLE)}
      </text>
      <text x={x + width - 14} y={y + 22} fontSize={11.5} textAnchor="end" fill="hsl(var(--muted-foreground))">
        {calls}
      </text>
      {group.caption && (
        <text x={x + 14} y={y + 41} fontSize={12} fill="hsl(var(--muted-foreground))">
          {fitText(group.caption, width - 28, FONT_BODY)}
        </text>
      )}
    </g>
  );
}

function Card({
  placed,
  selected,
  dimmed,
  onSelect,
  onOpen,
}: {
  placed: PlacedNode;
  selected: boolean;
  dimmed: boolean;
  onSelect: () => void;
  onOpen: () => void;
}) {
  const { node, x, y, width, height } = placed;
  const color = ROLE_COLOR[node.role];
  const Icon = KIND_ICON[node.kind] ?? CircleDot;
  const { lines, chips } = cardText(node);
  let chipX = x + 14;
  const chipY = y + height - 26;
  return (
    <g
      role="button"
      tabIndex={0}
      aria-label={`${ROLE_LABEL[node.role]}: ${node.title}`}
      aria-pressed={selected}
      data-node={node.id}
      className="cursor-pointer outline-none"
      opacity={dimmed ? 0.35 : 1}
      onPointerDown={(e) => e.stopPropagation()}
      onPointerUp={(e) => {
        e.stopPropagation();
        onSelect();
      }}
      onDoubleClick={(e) => {
        e.stopPropagation();
        onOpen();
      }}
      onKeyDown={(e) => {
        if (e.key === "Enter") onSelect();
      }}
    >
      <rect
        x={x}
        y={y}
        width={width}
        height={height}
        rx={9}
        fill="hsl(var(--card))"
        stroke={selected ? SELECT_COLOR : node.computed ? "hsl(var(--muted-foreground))" : "hsl(var(--border))"}
        strokeWidth={selected ? 2 : 1}
        strokeDasharray={node.computed ? "5 3" : undefined}
      />
      <rect x={x} y={y + 8} width={4} height={height - 16} rx={2} fill={color} />
      <Icon x={x + 14} y={y + 9} width={15} height={15} color={color} aria-hidden />
      <text x={x + 36} y={y + 21} fontSize={13} fontWeight={600} fill="hsl(var(--foreground))">
        {fitText(node.title, width - 48, FONT_TITLE)}
      </text>
      {lines.map((l, i) => (
        <text
          key={i}
          x={x + 16}
          y={y + 40 + i * 17}
          fontSize={l.font === FONT_MONO ? 11.5 : 12}
          fill={l.muted ? "hsl(var(--muted-foreground))" : "hsl(var(--foreground))"}
          className={l.mono ? "font-mono" : undefined}
        >
          {fitText(l.text, width - 28, l.font)}
        </text>
      ))}
      {chips.map((c) => {
        const text = fitText(c, 150, FONT_CHIP);
        const w = textWidth(text, FONT_CHIP) + 14;
        if (chipX + w > x + width - 8) return null;
        const cx = chipX;
        chipX += w + 5;
        return (
          <g key={c}>
            <rect x={cx} y={chipY} width={w} height={18} rx={9} fill="hsl(var(--muted))" stroke="hsl(var(--border))" />
            <text x={cx + 7} y={chipY + 13} fontSize={11} fill="hsl(var(--muted-foreground))">
              {text}
            </text>
          </g>
        );
      })}
    </g>
  );
}
