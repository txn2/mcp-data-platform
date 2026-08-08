import { useCallback, useMemo, useRef, useState, useEffect } from "react";
import { useVirtualizer } from "@tanstack/react-virtual";
import { ChevronRight, ChevronDown, Copy, Check } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  flattenJson,
  visibleNodes,
  allContainerPaths,
  defaultCollapsed,
  searchNodes,
  ancestorPaths,
  containerSummary,
  type JsonNode,
  type JsonValue,
} from "./model";

/** Row height in pixels; fixed so the virtualizer can size without measuring. */
const ROW_HEIGHT = 22;

/** Left inset per nesting level. */
const INDENT = 14;

/** Maximum height of the scrolling viewport, in pixels. */
const VIEWPORT_HEIGHT = 640;

interface JsonTreeProps {
  data: JsonValue;
  /** Search query from the toolbar; drives highlighting and jump-to-match. */
  query: string;
  /** Index of the match to reveal and scroll to. */
  activeMatch: number;
  /** Reports the current match set back to the toolbar. */
  onMatchesChange: (paths: string[]) => void;
  /** Reports the focused node's path so the toolbar can show the breadcrumb. */
  onSelectPath: (path: string) => void;
  selectedPath: string;
  /** Bumped by the toolbar to expand or collapse every container. */
  expandAllToken: number;
  collapseAllToken: number;
}

export function JsonTree({
  data,
  query,
  activeMatch,
  onMatchesChange,
  onSelectPath,
  selectedPath,
  expandAllToken,
  collapseAllToken,
}: JsonTreeProps) {
  const { nodes, truncated } = useMemo(() => flattenJson(data), [data]);
  const [collapsed, setCollapsed] = useState<Set<string>>(() => defaultCollapsed(nodes));

  // A new document needs a fresh collapse state, not the previous one's.
  useEffect(() => {
    setCollapsed(defaultCollapsed(nodes));
  }, [nodes]);

  useEffect(() => {
    if (expandAllToken > 0) setCollapsed(new Set());
  }, [expandAllToken]);

  useEffect(() => {
    if (collapseAllToken > 0) setCollapsed(allContainerPaths(nodes));
  }, [collapseAllToken, nodes]);

  const matches = useMemo(() => searchNodes(nodes, query), [nodes, query]);
  const matchPaths = useMemo(() => matches.map((m) => m.path), [matches]);

  useEffect(() => {
    onMatchesChange(matchPaths);
  }, [matchPaths, onMatchesChange]);

  const matchedPathSet = useMemo(() => new Set(matchPaths), [matchPaths]);

  const visible = useMemo(() => visibleNodes(nodes, collapsed), [nodes, collapsed]);

  const parentRef = useRef<HTMLDivElement>(null);
  const virtualizer = useVirtualizer({
    count: visible.length,
    getScrollElement: () => parentRef.current,
    estimateSize: () => ROW_HEIGHT,
    overscan: 24,
  });

  const toggle = useCallback((path: string) => {
    setCollapsed((prev) => {
      const next = new Set(prev);
      if (next.has(path)) next.delete(path);
      else next.add(path);
      return next;
    });
  }, []);

  // Jumping to a match has to reveal it first: a hit inside a collapsed subtree
  // is not in the visible list, so there is no row to scroll to until every
  // container between it and the root is open.
  const targetPath = matchPaths[activeMatch];
  useEffect(() => {
    if (!targetPath) return;
    setCollapsed((prev) => {
      const ancestors = ancestorPaths(nodes, targetPath);
      if (!ancestors.some((a) => prev.has(a))) return prev;
      const next = new Set(prev);
      for (const a of ancestors) next.delete(a);
      return next;
    });
  }, [targetPath, nodes]);

  useEffect(() => {
    if (!targetPath) return;
    const index = visible.findIndex((n) => n.path === targetPath);
    if (index >= 0) {
      virtualizer.scrollToIndex(index, { align: "center" });
      onSelectPath(targetPath);
    }
  }, [targetPath, visible, virtualizer, onSelectPath]);

  return (
    <div className="rounded-lg border bg-card" data-feedback-anchorable>
      {truncated && (
        <p className="border-b px-3 py-2 text-xs text-amber-600 dark:text-amber-400">
          This document is too large to show in full. The tree below holds the first portion; use the raw view or download for everything.
        </p>
      )}
      <div
        ref={parentRef}
        className="overflow-auto font-mono text-xs leading-[22px]"
        style={{ height: `min(70vh, ${VIEWPORT_HEIGHT}px)` }}
        role="tree"
        aria-label="JSON document"
      >
        <div style={{ height: virtualizer.getTotalSize(), position: "relative", width: "100%" }}>
          {virtualizer.getVirtualItems().map((item) => {
            const node = visible[item.index];
            if (!node) return null;
            return (
              <div
                key={node.path}
                style={{
                  position: "absolute",
                  top: 0,
                  left: 0,
                  width: "100%",
                  height: ROW_HEIGHT,
                  transform: `translateY(${item.start}px)`,
                }}
              >
                <JsonRow
                  node={node}
                  collapsed={collapsed.has(node.path)}
                  onToggle={toggle}
                  onSelect={onSelectPath}
                  selected={selectedPath === node.path}
                  matched={matchedPathSet.has(node.path)}
                  active={targetPath === node.path}
                  query={query}
                />
              </div>
            );
          })}
        </div>
      </div>
    </div>
  );
}

interface JsonRowProps {
  node: JsonNode;
  collapsed: boolean;
  onToggle: (path: string) => void;
  onSelect: (path: string) => void;
  selected: boolean;
  matched: boolean;
  active: boolean;
  query: string;
}

function JsonRow({ node, collapsed, onToggle, onSelect, selected, matched, active, query }: JsonRowProps) {
  const rowClass = [
    "flex items-center gap-1 whitespace-nowrap px-2 hover:bg-accent/40 cursor-default",
    selected ? "bg-accent/60" : "",
    active ? "ring-1 ring-inset ring-primary" : "",
  ]
    .filter(Boolean)
    .join(" ");

  // Only a matching row highlights: passing the query to every row would mark
  // incidental substrings in rows the search never selected.
  const rowQuery = matched ? query : "";

  return (
    <div
      className={rowClass}
      style={{ paddingLeft: 8 + node.depth * INDENT, height: ROW_HEIGHT }}
      onClick={() => onSelect(node.path)}
      role="treeitem"
      aria-expanded={node.container ? !collapsed : undefined}
      aria-selected={selected}
      tabIndex={-1}
    >
      <RowGutter node={node} collapsed={collapsed} onToggle={onToggle} />
      {node.key !== "" && (
        <span className="text-sky-700 dark:text-sky-300">
          <Highlight text={node.key} query={rowQuery} />
          <span className="text-muted-foreground">:</span>
        </span>
      )}
      {node.container ? (
        <ContainerBody node={node} collapsed={collapsed} />
      ) : (
        <ScalarValue node={node} query={rowQuery} />
      )}
    </div>
  );
}

/** The expand/collapse control, or the space one would occupy on a leaf. */
function RowGutter({
  node,
  collapsed,
  onToggle,
}: {
  node: JsonNode;
  collapsed: boolean;
  onToggle: (path: string) => void;
}) {
  if (!node.container) return <span className="w-3 shrink-0" />;

  const label = `${collapsed ? "Expand" : "Collapse"} ${node.key || "root"}`;
  return (
    <button
      type="button"
      aria-label={label}
      onClick={(e) => {
        e.stopPropagation();
        onToggle(node.path);
      }}
      className="shrink-0 rounded hover:bg-accent"
    >
      {collapsed ? <ChevronRight className="h-3 w-3" /> : <ChevronDown className="h-3 w-3" />}
    </button>
  );
}

/** A collapsed container shows its size; an expanded one shows its opening brace. */
function ContainerBody({ node, collapsed }: { node: JsonNode; collapsed: boolean }) {
  if (collapsed) return <span className="text-muted-foreground">{containerSummary(node)}</span>;
  return <span className="text-muted-foreground">{node.type === "array" ? "[" : "{"}</span>;
}

/** Type-aware value display: each JSON type reads differently at a glance. */
function ScalarValue({ node, query }: { node: JsonNode; query: string }) {
  const text = node.type === "string" ? JSON.stringify(node.value) : String(node.value);
  const tone =
    node.type === "string"
      ? "text-emerald-700 dark:text-emerald-300"
      : node.type === "number"
        ? "text-violet-700 dark:text-violet-300"
        : node.type === "boolean"
          ? "text-amber-700 dark:text-amber-400"
          : "text-muted-foreground italic";

  return (
    <span className={tone}>
      <Highlight text={text} query={query} />
    </span>
  );
}

/** Wraps every case-insensitive occurrence of the query in a highlight span. */
export function Highlight({ text, query }: { text: string; query: string }) {
  const q = query.trim();
  if (!q) return <>{text}</>;

  const parts: React.ReactNode[] = [];
  const lower = text.toLowerCase();
  const needle = q.toLowerCase();
  let cursor = 0;

  for (let at = lower.indexOf(needle); at !== -1; at = lower.indexOf(needle, cursor)) {
    if (at > cursor) parts.push(text.slice(cursor, at));
    parts.push(
      <mark key={at} className="rounded bg-amber-200 px-0.5 text-inherit dark:bg-amber-500/40">
        {text.slice(at, at + needle.length)}
      </mark>,
    );
    cursor = at + needle.length;
  }
  if (cursor < text.length) parts.push(text.slice(cursor));
  return <>{parts}</>;
}

/** A copy button that confirms in place rather than through a toast. */
export function CopyButton({ value, label }: { value: string; label: string }) {
  const [copied, setCopied] = useState(false);

  return (
    <Button
      type="button"
      variant="outline"
      size="xs"
      onClick={async () => {
        try {
          await navigator.clipboard.writeText(value);
          setCopied(true);
          setTimeout(() => setCopied(false), 1500);
        } catch {
          /* clipboard unavailable (insecure context); the button simply does nothing */
        }
      }}
      disabled={!value}
      title={label}
      aria-label={label}
    >
      {copied ? <Check className="text-emerald-600 dark:text-emerald-400" /> : <Copy />}
      {label}
    </Button>
  );
}
