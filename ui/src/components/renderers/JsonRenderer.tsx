import { lazy, Suspense, useCallback, useMemo, useState } from "react";
import { AlertTriangle, ChevronsDownUp, ChevronsUpDown, Search, ArrowUp, ArrowDown } from "lucide-react";
import { JsonTree, CopyButton } from "./json/JsonTree";
import { valueAtPath, type JsonValue } from "./json/model";

const CodeView = lazy(() => import("./CodeRenderer").then((m) => ({ default: m.CodeRenderer })));

type ViewMode = "tree" | "formatted" | "raw";

interface JsonRendererProps {
  content: string;
  fileName?: string;
}

/**
 * The JSON viewer: a searchable, collapsible tree over the parsed document,
 * with formatted and raw source views alongside it.
 *
 * The tree is virtualized, so a document of several megabytes stays responsive:
 * only the rows on screen are in the DOM. Content that does not parse falls
 * back to the raw view with the parser's message, which is more useful than an
 * empty tree.
 */
export function JsonRenderer({ content, fileName }: JsonRendererProps) {
  const parsed = useMemo(() => {
    try {
      return { value: JSON.parse(content) as JsonValue, error: null as string | null };
    } catch (err) {
      return { value: null, error: err instanceof Error ? err.message : "Invalid JSON" };
    }
  }, [content]);

  const [mode, setMode] = useState<ViewMode>("tree");
  const [query, setQuery] = useState("");
  const [matches, setMatches] = useState<string[]>([]);
  const [activeMatch, setActiveMatch] = useState(0);
  const [selectedPath, setSelectedPath] = useState("$");
  const [expandToken, setExpandToken] = useState(0);
  const [collapseToken, setCollapseToken] = useState(0);

  const handleMatches = useCallback((paths: string[]) => {
    setMatches(paths);
    setActiveMatch((prev) => (prev < paths.length ? prev : 0));
  }, []);

  const formatted = useMemo(() => {
    if (parsed.value === null && parsed.error) return content;
    try {
      return JSON.stringify(parsed.value, null, 2);
    } catch {
      return content;
    }
  }, [parsed, content]);

  const selectedValue = useMemo(
    () => (parsed.value === null && parsed.error ? "" : valueAtPath(parsed.value as JsonValue, selectedPath)),
    [parsed, selectedPath],
  );

  if (parsed.error) {
    return (
      <div className="space-y-3" data-feedback-anchorable>
        <div className="flex items-start gap-2 rounded-lg border border-amber-500/40 bg-amber-50 px-3 py-2 text-sm dark:bg-amber-950/30">
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-amber-600 dark:text-amber-400" />
          <div>
            <p className="font-medium">This content is not valid JSON</p>
            <p className="text-xs text-muted-foreground">{parsed.error}</p>
          </div>
        </div>
        <Suspense fallback={<pre className="rounded-lg border bg-card p-4 text-xs">{content}</pre>}>
          <CodeView content={content} language="json" fileName={fileName} />
        </Suspense>
      </div>
    );
  }

  const step = (delta: number) => {
    if (matches.length === 0) return;
    setActiveMatch((prev) => (prev + delta + matches.length) % matches.length);
  };

  return (
    <div className="space-y-2" data-feedback-anchorable>
      <div className="flex flex-wrap items-center gap-2">
        <div className="inline-flex rounded-md border text-xs">
          {(["tree", "formatted", "raw"] as ViewMode[]).map((m) => (
            <button
              key={m}
              type="button"
              onClick={() => setMode(m)}
              className={`px-3 py-1.5 capitalize transition-colors first:rounded-l-md last:rounded-r-md ${
                mode === m ? "bg-accent font-medium" : "hover:bg-accent/50"
              }`}
            >
              {m}
            </button>
          ))}
        </div>

        {mode === "tree" && (
          <>
            <button
              type="button"
              onClick={() => setExpandToken((t) => t + 1)}
              className="inline-flex items-center gap-1.5 rounded-md border px-2 py-1.5 text-xs hover:bg-accent"
            >
              <ChevronsUpDown className="h-3 w-3" />
              Expand all
            </button>
            <button
              type="button"
              onClick={() => setCollapseToken((t) => t + 1)}
              className="inline-flex items-center gap-1.5 rounded-md border px-2 py-1.5 text-xs hover:bg-accent"
            >
              <ChevronsDownUp className="h-3 w-3" />
              Collapse all
            </button>

            <div className="relative">
              <Search className="pointer-events-none absolute left-2 top-1/2 h-3 w-3 -translate-y-1/2 text-muted-foreground" />
              <input
                type="search"
                value={query}
                onChange={(e) => {
                  setQuery(e.target.value);
                  setActiveMatch(0);
                }}
                placeholder="Search keys and values"
                aria-label="Search keys and values"
                className="w-56 rounded-md border bg-transparent py-1.5 pl-7 pr-2 text-xs outline-none ring-ring focus:ring-2 dark:bg-input/30"
              />
            </div>

            {query.trim() !== "" && (
              <div className="flex items-center gap-1 text-xs text-muted-foreground">
                <span aria-live="polite">
                  {matches.length === 0 ? "No matches" : `${activeMatch + 1} of ${matches.length}`}
                </span>
                <button
                  type="button"
                  onClick={() => step(-1)}
                  disabled={matches.length === 0}
                  aria-label="Previous match"
                  className="rounded border p-1 hover:bg-accent disabled:opacity-40"
                >
                  <ArrowUp className="h-3 w-3" />
                </button>
                <button
                  type="button"
                  onClick={() => step(1)}
                  disabled={matches.length === 0}
                  aria-label="Next match"
                  className="rounded border p-1 hover:bg-accent disabled:opacity-40"
                >
                  <ArrowDown className="h-3 w-3" />
                </button>
              </div>
            )}
          </>
        )}
      </div>

      {mode === "tree" && (
        <>
          <div className="flex flex-wrap items-center gap-2 rounded-md border bg-muted/40 px-2 py-1.5">
            <code className="min-w-0 flex-1 truncate font-mono text-xs" title={selectedPath}>
              {selectedPath}
            </code>
            <CopyButton value={selectedPath} label="Copy path" />
            <CopyButton value={selectedValue} label="Copy value" />
          </div>

          <JsonTree
            data={parsed.value as JsonValue}
            query={query}
            activeMatch={activeMatch}
            onMatchesChange={handleMatches}
            onSelectPath={setSelectedPath}
            selectedPath={selectedPath}
            expandAllToken={expandToken}
            collapseAllToken={collapseToken}
          />
        </>
      )}

      {mode !== "tree" && (
        <Suspense fallback={<pre className="rounded-lg border bg-card p-4 text-xs">Loading...</pre>}>
          <CodeView content={mode === "formatted" ? formatted : content} language="json" fileName={fileName} />
        </Suspense>
      )}
    </div>
  );
}
