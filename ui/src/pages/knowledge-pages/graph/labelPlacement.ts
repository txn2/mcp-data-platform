/**
 * Label placement for the knowledge graph.
 *
 * Painting a label under every node produces an unreadable pile the moment two
 * nodes sit near each other — which a force layout guarantees, because it packs
 * related nodes together. This resolves the collisions instead: labels are
 * considered in priority order and one is drawn only if its box is still clear,
 * so the important labels always win and the rest are simply not drawn (they are
 * still reachable by hovering or selecting the node).
 */

/** LabelCandidate is a node's label with everything needed to place it. */
export interface LabelCandidate {
  id: string;
  text: string;
  x: number;
  y: number;
  /** The node's mark radius; the label sits below it. */
  radius: number;
  /** Higher is placed first and wins any collision. */
  priority: number;
}

/** CHAR_WIDTH approximates the advance of one character at the label's font size
 * (10px). SVG text has no cheap synchronous measurement, and over-estimating
 * slightly is the safe direction: it drops a borderline label rather than
 * drawing two on top of each other. */
const CHAR_WIDTH = 5.4;

/** LABEL_HEIGHT is the line box reserved for a label. */
const LABEL_HEIGHT = 12;

/** LABEL_GAP_Y is the distance from the node's centre to the label's top. */
export const LABEL_OFFSET_Y = 11;

/** Box is a placed label's axis-aligned rectangle in graph coordinates. */
interface Box {
  minX: number;
  maxX: number;
  minY: number;
  maxY: number;
}

/** labelBox returns the rectangle a candidate's label would occupy. */
function labelBox(c: LabelCandidate): Box {
  const halfWidth = (c.text.length * CHAR_WIDTH) / 2;
  const top = c.y + c.radius + LABEL_OFFSET_Y - LABEL_HEIGHT;
  return {
    minX: c.x - halfWidth,
    maxX: c.x + halfWidth,
    minY: top,
    maxY: top + LABEL_HEIGHT,
  };
}

/** overlaps reports whether two boxes intersect. */
function overlaps(a: Box, b: Box): boolean {
  return a.minX < b.maxX && b.minX < a.maxX && a.minY < b.maxY && b.minY < a.maxY;
}

/**
 * placeLabels returns the ids whose labels can be drawn without colliding,
 * highest priority first. `alwaysShow` labels are placed before everything else
 * and are never dropped: they are the ones the reader explicitly asked for (the
 * selection, a search match, a traced path), where hiding the label would look
 * like the feature failed rather than like decluttering.
 */
export function placeLabels(
  candidates: LabelCandidate[],
  alwaysShow: Set<string> = new Set(),
): Set<string> {
  const ordered = [...candidates].sort(
    (a, b) =>
      Number(alwaysShow.has(b.id)) - Number(alwaysShow.has(a.id)) ||
      b.priority - a.priority ||
      a.id.localeCompare(b.id),
  );

  const placed: Box[] = [];
  const shown = new Set<string>();
  for (const c of ordered) {
    if (!c.text) continue;
    const box = labelBox(c);
    if (alwaysShow.has(c.id)) {
      placed.push(box);
      shown.add(c.id);
      continue;
    }
    if (placed.some((p) => overlaps(p, box))) continue;
    placed.push(box);
    shown.add(c.id);
  }
  return shown;
}
