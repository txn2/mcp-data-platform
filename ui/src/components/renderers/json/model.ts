/**
 * The flattened model behind the JSON tree viewer.
 *
 * A JSON document is a tree, but a virtualized list needs a flat array of
 * exactly the rows currently visible. This module owns that translation: it
 * walks the parsed value once into a row per node, then derives the visible
 * slice from the set of collapsed paths. Keeping it separate from the component
 * means the flattening, path building and search can be tested directly.
 */

export type JsonValue = string | number | boolean | null | JsonValue[] | { [k: string]: JsonValue };

export type JsonNodeType = "object" | "array" | "string" | "number" | "boolean" | "null";

export interface JsonNode {
  /** JSONPath of this node, e.g. `$.results[0].name`. */
  path: string;
  /** The object key or array index this node sits under; "" at the root. */
  key: string;
  /** Nesting depth, 0 at the root. */
  depth: number;
  type: JsonNodeType;
  /** The scalar value, for leaf nodes. */
  value?: string | number | boolean | null;
  /** Child count, for containers. */
  childCount: number;
  /** True when the node is a container that can be collapsed. */
  container: boolean;
  /** Path of the parent container; "" at the root. */
  parentPath: string;
}

/** The type name used for a parsed value. */
export function jsonTypeOf(value: JsonValue): JsonNodeType {
  if (value === null) return "null";
  if (Array.isArray(value)) return "array";
  switch (typeof value) {
    case "object":
      return "object";
    case "number":
      return "number";
    case "boolean":
      return "boolean";
    default:
      return "string";
  }
}

/** An object key is bracket-quoted unless it is a plain identifier. */
export function appendKey(parent: string, key: string): string {
  return /^[A-Za-z_$][\w$]*$/.test(key) ? `${parent}.${key}` : `${parent}[${JSON.stringify(key)}]`;
}

/**
 * Walks a parsed JSON value into a depth-first array of nodes, one per key or
 * element. `maxNodes` bounds the walk so a pathological document cannot hang
 * the tab; the returned `truncated` flag tells the viewer to say so.
 */
export function flattenJson(
  root: JsonValue,
  maxNodes = 200_000,
): { nodes: JsonNode[]; truncated: boolean } {
  const nodes: JsonNode[] = [];
  let truncated = false;

  const walk = (value: JsonValue, path: string, key: string, depth: number, parentPath: string): void => {
    if (nodes.length >= maxNodes) {
      truncated = true;
      return;
    }
    const type = jsonTypeOf(value);

    if (type === "object" || type === "array") {
      const entries: Array<[string, JsonValue]> =
        type === "array"
          ? (value as JsonValue[]).map((v, i) => [String(i), v])
          : Object.entries(value as Record<string, JsonValue>);

      nodes.push({ path, key, depth, type, childCount: entries.length, container: true, parentPath });
      for (const [childKey, childValue] of entries) {
        const childPath = type === "array" ? `${path}[${childKey}]` : appendKey(path, childKey);
        walk(childValue, childPath, childKey, depth + 1, path);
      }
      return;
    }

    nodes.push({
      path,
      key,
      depth,
      type,
      value: value as string | number | boolean | null,
      childCount: 0,
      container: false,
      parentPath,
    });
  };

  walk(root, "$", "", 0, "");
  return { nodes, truncated };
}

/**
 * Returns the nodes to render given a set of collapsed container paths. A node
 * is hidden when any ancestor is collapsed, which is decided in one pass by
 * tracking the shallowest collapsed subtree currently in effect.
 */
export function visibleNodes(nodes: JsonNode[], collapsed: ReadonlySet<string>): JsonNode[] {
  const out: JsonNode[] = [];
  let hiddenUnder: string | null = null;
  let hiddenDepth = 0;

  for (const node of nodes) {
    if (hiddenUnder !== null) {
      if (node.depth > hiddenDepth) continue;
      hiddenUnder = null;
    }
    out.push(node);
    if (node.container && collapsed.has(node.path)) {
      hiddenUnder = node.path;
      hiddenDepth = node.depth;
    }
  }
  return out;
}

/** Every container path in the document, for "collapse all". */
export function allContainerPaths(nodes: JsonNode[]): Set<string> {
  const set = new Set<string>();
  for (const node of nodes) {
    if (node.container) set.add(node.path);
  }
  return set;
}

/**
 * Container paths collapsed by default: everything below the top level, so a
 * document opens showing its shape rather than every leaf at once.
 */
export function defaultCollapsed(nodes: JsonNode[], openDepth = 1): Set<string> {
  const set = new Set<string>();
  for (const node of nodes) {
    if (node.container && node.depth >= openDepth) set.add(node.path);
  }
  return set;
}

export interface SearchMatch {
  path: string;
  /** Whether the query matched the node's key or its value. */
  where: "key" | "value";
}

/**
 * Finds every node whose key or scalar value contains the query,
 * case-insensitively. Containers match on their key only: their "value" is
 * their subtree, which is searched through its own nodes.
 */
export function searchNodes(nodes: JsonNode[], query: string): SearchMatch[] {
  const q = query.trim().toLowerCase();
  if (!q) return [];

  const matches: SearchMatch[] = [];
  for (const node of nodes) {
    if (node.key.toLowerCase().includes(q)) {
      matches.push({ path: node.path, where: "key" });
      continue;
    }
    if (!node.container && node.value !== undefined && String(node.value).toLowerCase().includes(q)) {
      matches.push({ path: node.path, where: "value" });
    }
  }
  return matches;
}

/**
 * The ancestor container paths of a node, so revealing a search hit can expand
 * exactly the containers between it and the root.
 */
export function ancestorPaths(nodes: JsonNode[], path: string): string[] {
  const byPath = new Map(nodes.map((n) => [n.path, n]));
  const out: string[] = [];
  let cursor = byPath.get(path)?.parentPath ?? "";
  while (cursor) {
    out.push(cursor);
    cursor = byPath.get(cursor)?.parentPath ?? "";
  }
  return out;
}

/** The text copied by "copy value": scalars raw, containers as pretty JSON. */
export function valueAtPath(root: JsonValue, path: string): string {
  const value = resolvePath(root, path);
  if (value === undefined) return "";
  if (typeof value === "object" && value !== null) return JSON.stringify(value, null, 2);
  return String(value);
}

/** Walks a JSONPath produced by flattenJson back to its value. */
export function resolvePath(root: JsonValue, path: string): JsonValue | undefined {
  if (path === "$") return root;
  const steps = path.slice(1).match(/\[[^\]]*\]|\.[^.[]+/g);
  if (!steps) return undefined;

  let cursor: JsonValue | undefined = root;
  for (const step of steps) {
    if (cursor === null || cursor === undefined || typeof cursor !== "object") return undefined;
    const key = step.startsWith("[")
      ? unquote(step.slice(1, -1))
      : step.slice(1);
    cursor = Array.isArray(cursor)
      ? cursor[Number(key)]
      : (cursor as Record<string, JsonValue>)[key];
  }
  return cursor;
}

function unquote(raw: string): string {
  if (raw.startsWith('"')) {
    try {
      return JSON.parse(raw) as string;
    } catch {
      return raw;
    }
  }
  return raw;
}

/** A one-line preview of a container's contents, shown when it is collapsed. */
export function containerSummary(node: JsonNode): string {
  const unit = node.childCount === 1 ? "item" : "items";
  return node.type === "array" ? `[ ${node.childCount} ${unit} ]` : `{ ${node.childCount} ${unit} }`;
}
