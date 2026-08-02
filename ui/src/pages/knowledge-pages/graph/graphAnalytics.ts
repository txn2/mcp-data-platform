/**
 * Graph analysis for the knowledge-graph view (#1162). Drawing a force layout
 * shows that structure exists; these functions say what it IS — which entities
 * bridge otherwise separate topics, which pages form a cluster, and how any two
 * things in the corpus are connected. All of it is pure and index-based so it can
 * be tested against graphs whose answers are known by hand.
 */

/** Adjacency is the undirected neighbour index the traversals walk. */
export type Adjacency = Map<string, Set<string>>;

/** EdgeLike is the minimum an edge must expose to be analysed. */
export interface EdgeLike {
  source: string;
  target: string;
}

// ---------------------------------------------------------------------------
// Betweenness centrality (Brandes)
// ---------------------------------------------------------------------------

/** BrandesPass is one single-source shortest-path pass over the graph. */
interface BrandesPass {
  /** Nodes in non-decreasing distance order, for the reverse accumulation. */
  order: string[];
  /** Each node's predecessors on shortest paths from the source. */
  pred: Map<string, string[]>;
  /** How many shortest paths from the source reach each node. */
  sigma: Map<string, number>;
}

/** brandesBFS runs the forward breadth-first phase from one source. */
function brandesBFS(source: string, ids: string[], neighbors: Adjacency): BrandesPass {
  const pred = new Map<string, string[]>();
  const sigma = new Map<string, number>();
  const dist = new Map<string, number>();
  for (const v of ids) {
    pred.set(v, []);
    sigma.set(v, 0);
    dist.set(v, -1);
  }
  sigma.set(source, 1);
  dist.set(source, 0);

  const order: string[] = [];
  const queue = [source];
  for (let head = 0; head < queue.length; head++) {
    const v = queue[head]!;
    order.push(v);
    const dv = dist.get(v)!;
    for (const w of neighbors.get(v) ?? []) {
      if (dist.get(w)! < 0) {
        dist.set(w, dv + 1);
        queue.push(w);
      }
      if (dist.get(w) === dv + 1) {
        sigma.set(w, sigma.get(w)! + sigma.get(v)!);
        pred.get(w)!.push(v);
      }
    }
  }
  return { order, pred, sigma };
}

/** brandesAccumulate walks the pass in reverse, crediting each node's share. */
function brandesAccumulate(source: string, pass: BrandesPass, into: Map<string, number>) {
  const delta = new Map<string, number>();
  for (const v of pass.order) delta.set(v, 0);
  for (let i = pass.order.length - 1; i >= 0; i--) {
    const w = pass.order[i]!;
    const share = (1 + delta.get(w)!) / pass.sigma.get(w)!;
    for (const v of pass.pred.get(w)!) {
      delta.set(v, delta.get(v)! + pass.sigma.get(v)! * share);
    }
    if (w !== source) into.set(w, (into.get(w) ?? 0) + delta.get(w)!);
  }
}

/**
 * betweennessCentrality scores each node by how many shortest paths between
 * other nodes run through it — the standard measure of a bridge. An entity cited
 * by two otherwise unrelated clusters of pages scores high; a leaf scores zero.
 * This is the quantity that answers "which entities bridge topics", which a
 * force layout only hints at.
 *
 * Brandes' algorithm, O(V*E) on an unweighted graph. Scores are the raw pair
 * counts, halved because an undirected graph reaches each pair from both ends.
 */
export function betweennessCentrality(ids: string[], neighbors: Adjacency): Map<string, number> {
  const scores = new Map<string, number>(ids.map((id) => [id, 0]));
  for (const source of ids) {
    brandesAccumulate(source, brandesBFS(source, ids, neighbors), scores);
  }
  for (const [id, score] of scores) scores.set(id, score / 2);
  return scores;
}

// ---------------------------------------------------------------------------
// Community detection (Louvain)
// ---------------------------------------------------------------------------

/** MAX_LOUVAIN_LEVELS bounds the aggregation phases. Real corpora converge in
 * two or three; the bound is a backstop against a pathological input looping. */
const MAX_LOUVAIN_LEVELS = 10;

/** WeightedGraph is an index-keyed adjacency with per-node self-loop weight,
 * which is what the aggregation phase produces and consumes. */
interface WeightedGraph {
  adj: Map<number, number>[];
  /** Weight of edges collapsed INTO each node by a previous aggregation. */
  self: number[];
}

/** addWeight accumulates an undirected edge weight in one direction. */
function addWeight(adj: Map<number, number>[], from: number, to: number, w: number) {
  adj[from]!.set(to, (adj[from]!.get(to) ?? 0) + w);
}

/** buildWeighted turns the id-keyed edge list into an index-keyed graph. */
function buildWeighted(ids: string[], edges: EdgeLike[]): WeightedGraph {
  const index = new Map(ids.map((id, i) => [id, i]));
  const adj: Map<number, number>[] = ids.map(() => new Map());
  for (const e of edges) {
    const a = index.get(e.source);
    const b = index.get(e.target);
    if (a === undefined || b === undefined || a === b) continue;
    addWeight(adj, a, b, 1);
    addWeight(adj, b, a, 1);
  }
  return { adj, self: ids.map(() => 0) };
}

/** degrees returns each node's weighted degree, counting a self-loop twice. */
function degrees(g: WeightedGraph): number[] {
  return g.adj.map((m, i) => {
    let k = 2 * g.self[i]!;
    for (const w of m.values()) k += w;
    return k;
  });
}

/**
 * louvainPass moves each node to the neighbouring community that most improves
 * modularity, repeating until nothing moves. Returns null when the pass changed
 * nothing, which is the signal that the whole algorithm has converged. Nodes are
 * visited in index order, so the result is deterministic for a given input.
 */
function louvainPass(g: WeightedGraph): number[] | null {
  const k = degrees(g);
  const m2 = k.reduce((a, b) => a + b, 0);
  if (m2 === 0) return null;
  const community = g.adj.map((_, i) => i);
  const sumTot = k.slice();
  let improved = false;

  for (let sweep = 0, moved = true; moved && sweep < MAX_LOUVAIN_LEVELS; sweep++) {
    moved = false;
    for (let i = 0; i < g.adj.length; i++) {
      const from = community[i]!;
      sumTot[from]! -= k[i]!;
      const best = bestCommunity(g, community, sumTot, k, i, m2);
      sumTot[best]! += k[i]!;
      if (best !== from) {
        community[i] = best;
        moved = true;
        improved = true;
      }
    }
  }
  return improved ? community : null;
}

/** bestCommunity picks the community for node i with the highest modularity
 * gain, defaulting to the one it is already in when no move improves. */
function bestCommunity(
  g: WeightedGraph,
  community: number[],
  sumTot: number[],
  k: number[],
  i: number,
  m2: number,
): number {
  const weightTo = new Map<number, number>();
  for (const [j, w] of g.adj[i]!) {
    if (j === i) continue;
    const c = community[j]!;
    weightTo.set(c, (weightTo.get(c) ?? 0) + w);
  }
  const current = community[i]!;
  let best = current;
  let bestGain = (weightTo.get(current) ?? 0) - (sumTot[current]! * k[i]!) / m2;
  for (const [c, w] of weightTo) {
    const gain = w - (sumTot[c]! * k[i]!) / m2;
    if (gain > bestGain) {
      bestGain = gain;
      best = c;
    }
  }
  return best;
}

/** renumber compacts arbitrary community labels to 0..k-1 in first-seen order,
 * so the labels are usable as array indices and as a stable colour key. */
function renumber(labels: number[]): { labels: number[]; count: number } {
  const dense = new Map<number, number>();
  const out = labels.map((l) => {
    let d = dense.get(l);
    if (d === undefined) {
      d = dense.size;
      dense.set(l, d);
    }
    return d;
  });
  return { labels: out, count: dense.size };
}

/** aggregate collapses each community into a single node for the next level. */
function aggregate(g: WeightedGraph, labels: number[], count: number): WeightedGraph {
  const adj: Map<number, number>[] = Array.from({ length: count }, () => new Map());
  const self = new Array<number>(count).fill(0);
  for (let i = 0; i < g.adj.length; i++) {
    const ci = labels[i]!;
    self[ci]! += g.self[i]!;
    for (const [j, w] of g.adj[i]!) {
      const cj = labels[j]!;
      // Each internal edge is seen from both ends, so halve it into the loop.
      if (ci === cj) self[ci]! += w / 2;
      else addWeight(adj, ci, cj, w);
    }
  }
  return { adj, self };
}

/**
 * louvainCommunities groups the graph into modularity-maximizing communities —
 * the clusters a reader perceives as "topics". Returns node id -> community
 * index; an isolated node gets its own. Deterministic for a given node order.
 */
export function louvainCommunities(ids: string[], edges: EdgeLike[]): Map<string, number> {
  let graph = buildWeighted(ids, edges);
  let assignment = ids.map((_, i) => i);
  for (let level = 0; level < MAX_LOUVAIN_LEVELS; level++) {
    const pass = louvainPass(graph);
    if (!pass) break;
    const { labels, count } = renumber(pass);
    assignment = assignment.map((c) => labels[c]!);
    if (count === graph.adj.length) break; // nothing merged; further levels cannot
    graph = aggregate(graph, labels, count);
  }
  const { labels } = renumber(assignment);
  return new Map(ids.map((id, i) => [id, labels[i]!]));
}

/**
 * modularity scores a partition in [-1, 1]: how much more of the graph's edge
 * weight falls inside communities than chance would put there. Used to report
 * whether the corpus actually has cluster structure rather than asserting it.
 */
export function modularity(ids: string[], edges: EdgeLike[], communities: Map<string, number>): number {
  const g = buildWeighted(ids, edges);
  const k = degrees(g);
  const m2 = k.reduce((a, b) => a + b, 0);
  if (m2 === 0) return 0;
  const index = new Map(ids.map((id, i) => [id, i]));
  let inside = 0;
  let expected = 0;
  const totals = new Map<number, number>();
  for (const id of ids) {
    const c = communities.get(id) ?? -1;
    totals.set(c, (totals.get(c) ?? 0) + k[index.get(id)!]!);
  }
  for (const e of edges) {
    // A self-loop is excluded from the degrees by buildWeighted, so counting it
    // as internal weight here would inflate the score past its [-1, 1] range.
    if (e.source === e.target) continue;
    if (communities.get(e.source) === communities.get(e.target)) inside += 2;
  }
  for (const total of totals.values()) expected += (total / m2) ** 2;
  return inside / m2 - expected;
}

// ---------------------------------------------------------------------------
// Traversal
// ---------------------------------------------------------------------------

/**
 * neighborhood returns the roots plus everything within `depth` hops — the set
 * a focused view draws. Depth 0 is the roots alone.
 */
export function neighborhood(neighbors: Adjacency, roots: Iterable<string>, depth: number): Set<string> {
  const seen = new Set<string>(roots);
  let frontier = [...seen];
  for (let d = 0; d < depth; d++) {
    const next: string[] = [];
    for (const v of frontier) {
      for (const w of neighbors.get(v) ?? []) {
        if (seen.has(w)) continue;
        seen.add(w);
        next.push(w);
      }
    }
    if (next.length === 0) break;
    frontier = next;
  }
  return seen;
}

/**
 * shortestPath returns the node ids from `from` to `to` inclusive, or null when
 * they are not connected. This is the question a list of citations cannot
 * answer: how a page and a distant entity are related at all.
 */
export function shortestPath(neighbors: Adjacency, from: string, to: string): string[] | null {
  if (from === to) return [from];
  const cameFrom = new Map<string, string>([[from, from]]);
  const queue = [from];
  for (let head = 0; head < queue.length; head++) {
    const v = queue[head]!;
    for (const w of neighbors.get(v) ?? []) {
      if (cameFrom.has(w)) continue;
      cameFrom.set(w, v);
      if (w === to) return tracePath(cameFrom, from, to);
      queue.push(w);
    }
  }
  return null;
}

/** tracePath walks the predecessor map back from `to` and reverses it. */
function tracePath(cameFrom: Map<string, string>, from: string, to: string): string[] {
  const path = [to];
  let cur = to;
  while (cur !== from) {
    cur = cameFrom.get(cur)!;
    path.push(cur);
  }
  return path.reverse();
}

/** pathEdgeKey identifies an undirected edge on a path, order-independent, so
 * the canvas can tell whether an edge it is drawing lies on the highlighted path. */
export function pathEdgeKey(a: string, b: string): string {
  return a < b ? `${a} ${b}` : `${b} ${a}`;
}

/** pathEdgeKeys returns the edge keys of every hop along a path. */
export function pathEdgeKeys(path: string[]): Set<string> {
  const keys = new Set<string>();
  for (let i = 1; i < path.length; i++) keys.add(pathEdgeKey(path[i - 1]!, path[i]!));
  return keys;
}
