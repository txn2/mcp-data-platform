import type {
  FlowNode,
  FlowNodeRun,
  FlowStructure,
  ScriptFlow,
  StructBox,
  StructEdge,
  StructNode,
} from "@/api/portal/hooks/scriptFlow";

// The pure half of the Structure view (#1972): what each node says, which
// helper boxes are folded, what a selection lights up, and how a run reads on
// the picture. The drawing reads everything it needs from here.

// ViewNode is a node as drawn: a structure node, or a folded helper box drawn
// as one node in its place.
export type ViewNode =
  | (StructNode & { folded?: undefined })
  | { id: string; kind: "fn"; label: string; caption?: string; box?: string; line: number; folded: StructBox };

export interface StructureView {
  nodes: ViewNode[];
  edges: StructEdge[];
  boxes: StructBox[];
}

// COLLAPSE_AT is how many nodes a helper box holds before it opens folded.
export const COLLAPSE_AT = 8;

// ancestors is every box a box or node sits in, innermost first.
function ancestors(s: FlowStructure, box: string | undefined): string[] {
  const parent = new Map(s.boxes.map((b) => [b.id, b.parent]));
  const out: string[] = [];
  for (let b = box; b; b = parent.get(b)) out.push(b);
  return out;
}

// nodesIn counts the nodes a box holds, nested boxes included.
export function nodesIn(s: FlowStructure, boxId: string): number {
  return s.nodes.filter((n) => ancestors(s, n.box).includes(boxId)).length;
}

// defaultFolded is the helper boxes that open folded: those holding more than
// COLLAPSE_AT nodes, except the ones around the node or box a run failed at,
// which the reader came to see.
export function defaultFolded(s: FlowStructure, failed?: string): Set<string> {
  const open = new Set<string>();
  if (failed) {
    const n = s.nodes.find((x) => x.id === failed);
    const b = s.boxes.find((x) => x.id === failed);
    for (const a of ancestors(s, n ? n.box : b?.id)) open.add(a);
  }
  const out = new Set<string>();
  for (const b of s.boxes) {
    if (b.kind === "function" && !open.has(b.id) && nodesIn(s, b.id) > COLLAPSE_AT) out.add(b.id);
  }
  return out;
}

// fold draws each folded box as one node: whatever it holds is replaced by
// it, and the edges into and out of what it holds are its edges.
export function fold(s: FlowStructure, folded: Set<string>): StructureView {
  const outermost = (box: string | undefined): string | undefined => {
    let hit: string | undefined;
    for (const b of ancestors(s, box)) if (folded.has(b)) hit = b;
    return hit;
  };
  const rep = new Map<string, string>();
  const nodes: ViewNode[] = [];
  for (const n of s.nodes) {
    const f = outermost(n.box);
    if (f) rep.set(n.id, f);
    else nodes.push(n);
  }
  const boxes: StructBox[] = [];
  for (const b of s.boxes) {
    const f = outermost(b.id);
    if (!f) boxes.push(b);
    else if (f === b.id && !outermost(b.parent)) nodes.push(foldedNode(b));
  }
  return { nodes, edges: foldEdges(s.edges, rep), boxes };
}

// foldedNode is a folded helper box drawn as one node.
function foldedNode(b: StructBox): ViewNode {
  return { id: b.id, kind: "fn", label: b.label, caption: b.caption, box: b.parent, line: b.line, folded: b };
}

// foldEdges moves each edge's ends onto the folded box that holds them,
// dropping the edges that fall inside one and keeping one of each left.
function foldEdges(edges: StructEdge[], rep: Map<string, string>): StructEdge[] {
  const seen = new Set<string>();
  const out: StructEdge[] = [];
  for (const e of edges) {
    const from = rep.get(e.from) ?? e.from;
    const to = rep.get(e.to) ?? e.to;
    const key = `${from}>${to}>${e.label ?? ""}`;
    if (from === to || seen.has(key)) continue;
    seen.add(key);
    out.push({ ...e, from, to });
  }
  return out;
}

// FIXED_TEXT is what the nodes that carry no label say.
const FIXED_TEXT: Partial<Record<ViewNode["kind"], string>> = {
  start: "Start",
  end: "End",
  return: "Returns early",
};

// nodeText is what a node says, in the words agreed for #1972.
export function nodeText(n: ViewNode, graph?: ScriptFlow): string {
  const fixed = FIXED_TEXT[n.kind];
  if (fixed) return fixed;
  if (n.kind === "stop") return n.label ? `Stops: ${n.label}` : "Stops";
  if (n.kind === "if") return `If ${n.label ?? ""}`;
  if (n.kind === "fn") return n.label;
  return stepCard(n, graph)?.title ?? n.label ?? "platform call";
}

// stepCard is the value graph's card a step node draws.
export function stepCard(n: ViewNode, graph?: ScriptFlow): FlowNode | undefined {
  if (n.kind !== "step" || !n.step || !graph) return undefined;
  return graph.nodes.find((c) => c.id === n.step);
}

// boxText is a box's heading: a loop says it repeats, a function its
// signature.
export function boxText(b: StructBox): string {
  return b.kind === "loop" ? `Repeats: ${b.label}` : b.label;
}

// neighbors is the cards a card's inputs came from and the cards its result
// feeds, in the value graph: what selecting a call lights in the Structure
// view.
export function neighbors(graph: ScriptFlow, step: string): Set<string> {
  const out = new Set<string>([step]);
  for (const e of graph.edges) {
    if (e.kind !== "data") continue;
    if (e.from === step) out.add(e.to);
    if (e.to === step) out.add(e.from);
  }
  return out;
}

// loopCalls is how many times a loop's calls ran in a run: the most calls any
// one call inside it made. Which iterations made no call is not recorded, so
// this is what the run's calls say, not a count of iterations.
export function loopCalls(s: FlowStructure, boxId: string, run?: Record<string, FlowNodeRun>): number {
  if (!run) return 0;
  let most = 0;
  for (const n of s.nodes) {
    if (n.box === boxId && n.kind === "step" && n.step) most = Math.max(most, run[n.step]?.calls ?? 0);
  }
  return most;
}

// RunState is how a node reads with a run drawn.
export type RunState = "ran" | "skipped" | "failed" | "plain";

// DrawnRun is what a node's reading needs of the drawn run.
export interface DrawnRun {
  nodes: Record<string, FlowNodeRun>;
  status: string;
  structure_failed?: string;
  unplaced: boolean;
}

// runState is whether the drawn run reached a node. Only a call is known to
// have run or not; a decision or an early return is drawn plainly, since which
// arm a run took is not recorded. The end ran when the run succeeded, and a
// stop is lighter unless the run stopped there.
export function runState(n: ViewNode, run: DrawnRun | undefined, s: FlowStructure): RunState {
  const failed = n.kind === "fn" ? failedWithin(s, n.id, run?.structure_failed) : run?.structure_failed === n.id;
  if (failed) return "failed";
  if (!run || run.unplaced) return "plain";
  return reached(n, run, s);
}

// reached reads a node the run did not fail at.
function reached(n: ViewNode, run: DrawnRun, s: FlowStructure): RunState {
  const ran = (step?: string) => !!step && !!run.nodes[step]?.reached;
  switch (n.kind) {
    case "step":
      return ran(n.step) ? "ran" : "skipped";
    case "end":
      return run.status === "succeeded" ? "ran" : "skipped";
    case "stop":
      return "skipped";
    case "fn":
      return s.nodes.some((x) => x.kind === "step" && ancestors(s, x.box).includes(n.id) && ran(x.step)) ? "ran" : "skipped";
    default:
      return "plain";
  }
}

// failedWithin reports whether the run failed inside a box, at a node or a
// box it holds.
export function failedWithin(s: FlowStructure, boxId: string, failed?: string): boolean {
  if (!failed) return false;
  if (failed === boxId) return true;
  const n = s.nodes.find((x) => x.id === failed);
  const b = s.boxes.find((x) => x.id === failed);
  return ancestors(s, n ? n.box : b?.parent).includes(boxId);
}

// untested reports whether a node's line is one the version's tests do not
// reach.
export function untested(n: ViewNode, missed: Set<number> | undefined): boolean {
  if (!missed || n.kind === "start" || n.kind === "end" || n.kind === "fn") return false;
  return missed.has(n.line);
}
