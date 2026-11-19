import { useLayoutEffect, useRef, useState } from "react";
import type {
  ApplicationDiagram,
  ApplicationDiagramLink,
  ApplicationDiagramNode,
} from "@/api/portal/hooks/applications";
import { SELECT_COLOR } from "@/pages/scripts/flow/flowModel";
import { NODE_COLOR, NODE_KIND_LABEL, changeColor } from "./diagramModel";
import {
  isTableLink,
  rowNote,
  type Related,
  type RowFacts,
  type RowNote,
} from "./diagramScale";
import {
  ROW_HEIGHT,
  lanes,
  linkPath,
  type Box,
  type LargeLayout,
  type PlacedRow,
} from "./largeLayout";

// LargeDiagram is a large application drawn (#2700): its parts as rows in
// four columns, the automations and the tables in their boxes, every link
// that is not an automation reading or writing a table drawn, and in place
// of those one band between the boxes. Selecting a row draws its links and
// dims what it does not reach; comparing draws the links that were added or
// removed without a selection.

interface Props {
  diagram: ApplicationDiagram;
  layout: LargeLayout;
  facts: RowFacts;
  selected: string | null;
  onSelect: (id: string | null) => void;
}

const MUTED = "hsl(var(--muted-foreground))";

// MIN_SCALE is the smallest the drawing is shrunk to fit its column; below it
// the drawing scrolls sideways, and a phone opens the Table view instead.
const MIN_SCALE = 0.6;

/**
 * useFitScale is how much the drawing is shrunk so its four columns fit the
 * width it is given, the tables box included (#2700): a person sees which
 * tables an automation reaches without scrolling sideways. Never enlarged.
 */
function useFitScale(width: number) {
  const ref = useRef<HTMLDivElement>(null);
  const [scale, setScale] = useState(1);
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    const fit = () => {
      const room = el.clientWidth;
      setScale(room > 0 ? Math.max(MIN_SCALE, Math.min(1, room / width)) : 1);
    };
    fit();
    if (typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(fit);
    observer.observe(el);
    return () => observer.disconnect();
  }, [width]);
  return { ref, scale };
}
const WRITE_WIDTH = 2.8;
const READ_WIDTH = 1.3;

export function LargeDiagram({
  diagram,
  layout,
  facts,
  selected,
  onSelect,
}: Props) {
  const byId = new Map(diagram.nodes.map((n) => [n.id, n]));
  const drawn = diagram.links.filter(
    (l) =>
      layout.rows.has(l.from) &&
      layout.rows.has(l.to) &&
      shown(l, byId, selected),
  );
  const lane = lanes(drawn);
  const reachers = facts.reach.byScript.size;
  // A table named only when it runs is no one table: the band counts the others.
  const reached = [...facts.reach.byTable.keys()].filter(
    (id) => !byId.get(id)?.computed,
  ).length;
  const fit = useFitScale(layout.width);
  return (
    <div
      ref={fit.ref}
      className="max-h-[640px] overflow-auto rounded-md border bg-card"
      data-testid="application-diagram-large"
      data-scale={fit.scale.toFixed(2)}
      onKeyDown={(e) => e.key === "Escape" && onSelect(null)}
    >
      <div
        style={{
          width: layout.width * fit.scale,
          height: layout.height * fit.scale,
        }}
      >
        <div
          role="group"
          aria-label="Application diagram"
          className="relative origin-top-left"
          style={{
            width: layout.width,
            height: layout.height,
            transform: fit.scale < 1 ? `scale(${fit.scale})` : undefined,
          }}
          onClick={(e) => e.target === e.currentTarget && onSelect(null)}
        >
          {layout.boxes.map((b) => (
            <BoxFrame key={b.label} box={b} />
          ))}
          <svg
            aria-hidden="true"
            width={layout.width}
            height={layout.height}
            className="pointer-events-none absolute inset-0"
          >
            <defs>
              {[
                ["muted", MUTED],
                ["lit", SELECT_COLOR],
                ["added", changeColor("added")!],
                ["removed", changeColor("removed")!],
              ].map(([id, color]) => (
                <marker
                  key={id}
                  id={`large-arrow-${id}`}
                  orient="auto"
                  markerWidth="8"
                  markerHeight="8"
                  refX="7"
                  refY="4"
                >
                  <path d="M0 0 L8 4 L0 8 Z" fill={color} />
                </marker>
              ))}
            </defs>
            {!selected && reachers > 0 && <Band layout={layout} />}
            {drawn.map((l) => (
              <LinkLine
                key={`${l.from}->${l.to}:${l.kind}`}
                link={l}
                from={layout.rows.get(l.from)!}
                to={layout.rows.get(l.to)!}
                lane={lane.get(l) ?? 0}
                lit={l.from === selected || l.to === selected}
                dim={
                  selected !== null && l.from !== selected && l.to !== selected
                }
                table={isTableLink(l, byId)}
              />
            ))}
          </svg>
          {!selected && reachers > 0 && (
            <p
              className="pointer-events-none absolute w-[190px] text-center text-[11px] leading-[15px] text-muted-foreground"
              style={{
                left: (layout.band.x1 + layout.band.x2) / 2 - 95,
                top: layout.band.y - 40,
              }}
              data-testid="diagram-band"
            >
              {reachers} automations reach {reached} tables. Select one to see
              which.
            </p>
          )}
          {[...layout.rows.values()].map((r) => (
            <Row
              key={r.node.id}
              placed={r}
              note={rowNote(r.node, facts)}
              selected={selected === r.node.id}
              dim={dimmed(facts.related, r.node.id)}
              unwritten={facts.unwritten.has(r.node.id)}
              onSelect={() => onSelect(r.node.id)}
            />
          ))}
        </div>
      </div>
    </div>
  );
}

// shown reports whether a link is drawn: every link that is not an automation
// reading or writing a table, and of those the selection's and a comparison's
// added or removed ones.
function shown(
  l: ApplicationDiagramLink,
  byId: Map<string, ApplicationDiagramNode>,
  selected: string | null,
): boolean {
  if (!isTableLink(l, byId)) return true;
  if (selected !== null && (l.from === selected || l.to === selected))
    return true;
  return l.change === "added" || l.change === "removed";
}

function dimmed(related: Related | null, id: string): boolean {
  return related !== null && !related.lit.has(id);
}

function BoxFrame({ box }: { box: Box }) {
  return (
    <div
      className="pointer-events-none absolute rounded-xl border bg-muted/50"
      style={{ left: box.x, top: box.y, width: box.width, height: box.height }}
    >
      <p className="px-3 pt-2.5 text-[10.5px] font-semibold tracking-wide text-muted-foreground uppercase">
        {box.label}
        {box.detail && (
          <span className="ml-1.5 text-[13px] tracking-normal text-foreground normal-case">
            {box.detail}
          </span>
        )}
        {` · ${box.count}`}
      </p>
    </div>
  );
}

function Band({ layout }: { layout: LargeLayout }) {
  const { x1, x2, y } = layout.band;
  return (
    <path
      d={`M${x1} ${y} H${x2}`}
      stroke={MUTED}
      strokeWidth={10}
      strokeOpacity={0.3}
      strokeLinecap="round"
      data-testid="diagram-band-line"
    />
  );
}

interface LineProps {
  link: ApplicationDiagramLink;
  from: PlacedRow;
  to: PlacedRow;
  lane: number;
  lit: boolean;
  dim: boolean;
  table: boolean;
}

// lineStroke is a link's color and arrowhead: lit with the selection, else
// its comparison's color, else muted.
function lineStroke(
  link: ApplicationDiagramLink,
  lit: boolean,
): { color: string; marker: string } {
  if (lit) return { color: SELECT_COLOR, marker: "lit" };
  if (link.change === "added" || link.change === "removed")
    return { color: changeColor(link.change)!, marker: link.change };
  return { color: MUTED, marker: "muted" };
}

function lineWidth(link: ApplicationDiagramLink, table: boolean): number {
  if (!table) return 1.4;
  return link.kind === "writes" ? WRITE_WIDTH : READ_WIDTH;
}

function LinkLine({ link, from, to, lane, lit, dim, table }: LineProps) {
  const { color, marker } = lineStroke(link, lit);
  const dashed = link.change === "removed" || to.node.computed;
  return (
    <path
      d={linkPath(from, to, lane)}
      fill="none"
      stroke={color}
      strokeWidth={lineWidth(link, table)}
      strokeOpacity={dim ? 0.18 : 0.9}
      strokeDasharray={dashed ? "5 3" : undefined}
      markerEnd={`url(#large-arrow-${marker})`}
      data-link={`${link.from}->${link.to}`}
      data-kind={link.kind}
      data-change={link.change}
    />
  );
}

const NOTE_CLASS: Record<RowNote["tone"], string> = {
  plain: "text-muted-foreground",
  writes:
    "rounded-full bg-amber-100 px-1.5 font-semibold text-amber-900 dark:bg-amber-500/20 dark:text-amber-200",
  reads:
    "rounded-full border border-amber-400 px-1.5 text-amber-900 dark:text-amber-200",
  warn: "font-semibold text-amber-800 dark:text-amber-300",
  added: "rounded-full px-1.5 font-semibold text-white",
  removed: "rounded-full px-1.5 font-semibold text-white",
  changed: "rounded-full px-1.5 font-semibold text-white",
};

function noteStyle(note: RowNote): React.CSSProperties | undefined {
  if (
    note.tone === "added" ||
    note.tone === "removed" ||
    note.tone === "changed"
  )
    return { background: changeColor(note.tone) };
  return undefined;
}

interface RowProps {
  placed: PlacedRow;
  note: RowNote | null;
  selected: boolean;
  dim: boolean;
  unwritten: boolean;
  onSelect: () => void;
}

// rowBorder is a row's outline: the selection's color when selected or
// reached, a warning for a table nothing writes, a comparison's color.
function rowBorder(
  node: ApplicationDiagramNode,
  selected: boolean,
  tagged: boolean,
  unwritten: boolean,
): string {
  if (selected || tagged) return SELECT_COLOR;
  if (unwritten) return "hsl(38 92% 45%)";
  return changeColor(node.change) ?? "hsl(var(--border))";
}

const isChanged = (node: ApplicationDiagramNode) =>
  node.change !== undefined && node.change !== "unchanged";

// rowStyle is where a row sits and how it is outlined: thicker when selected,
// written by the selection, a table nothing writes or a compared change;
// dashed for a table named when it runs or a removed part.
function rowStyle({
  placed,
  note,
  selected,
  dim,
  unwritten,
}: Omit<RowProps, "onSelect">): React.CSSProperties {
  const { node, x, y, width } = placed;
  return {
    left: x,
    top: y,
    width,
    height: ROW_HEIGHT,
    opacity: dim ? 0.35 : 1,
    border: rowOutline(node, note, selected, unwritten),
    boxShadow: `inset 3px 0 0 ${NODE_COLOR[node.kind] ?? MUTED}`,
    fontWeight: selected ? 600 : undefined,
  };
}

// rowOutline is a row's border shorthand.
function rowOutline(
  node: ApplicationDiagramNode,
  note: RowNote | null,
  selected: boolean,
  unwritten: boolean,
): string {
  const tone = note?.tone;
  const tagged = tone === "writes" || tone === "reads";
  const strong = selected || tone === "writes" || unwritten || isChanged(node);
  const line = node.computed || node.change === "removed" ? "dashed" : "solid";
  return `${strong ? 2 : 1}px ${line} ${rowBorder(node, selected, tagged, unwritten)}`;
}

function Row(props: RowProps) {
  const { placed, note, selected, onSelect } = props;
  const node = placed.node;
  return (
    <button
      type="button"
      aria-pressed={selected}
      aria-label={`${NODE_KIND_LABEL[node.kind] ?? node.kind} ${node.label}${note ? `: ${note.text}` : ""}`}
      data-node={node.id}
      data-change={node.change}
      className="absolute flex items-center gap-1.5 rounded-[5px] bg-card pr-2 pl-2.5 text-left text-xs text-foreground outline-none focus-visible:ring-2 focus-visible:ring-ring"
      style={rowStyle(props)}
      onClick={(e) => {
        e.stopPropagation();
        onSelect();
      }}
    >
      <span
        className={`min-w-0 flex-1 truncate ${node.change === "removed" ? "line-through" : ""}`}
      >
        {rowLabel(node)}
      </span>
      {note && (
        <span
          className={`shrink-0 text-[10.5px] whitespace-nowrap ${NOTE_CLASS[note.tone]}`}
          style={noteStyle(note)}
        >
          {note.text}
        </span>
      )}
    </button>
  );
}

// rowLabel is a row's name: a table named when it runs says which connection
// it is on, since there is one per automation and connection.
function rowLabel(node: ApplicationDiagramNode): string {
  if (node.computed && node.detail[0])
    return `${node.label} · ${node.detail[0]}`;
  return node.label;
}
