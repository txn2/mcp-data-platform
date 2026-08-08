import { lazy, Suspense, useCallback, useMemo, useState } from "react";
import { AlertTriangle, ChevronsDownUp, ChevronsUpDown, ArrowUp, ArrowDown } from "lucide-react";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { SearchInput } from "@/components/patterns/SearchInput";
import { SegmentedControl, type SegmentedOption } from "@/components/patterns/SegmentedControl";
import { JsonTree, CopyButton } from "./json/JsonTree";
import { valueAtPath, type JsonValue } from "./json/model";

const CodeView = lazy(() => import("./CodeRenderer").then((m) => ({ default: m.CodeRenderer })));

type ViewMode = "tree" | "formatted" | "raw";

const VIEW_MODES: SegmentedOption<ViewMode>[] = [
  { value: "tree", label: "Tree", text: "Tree" },
  { value: "formatted", label: "Formatted", text: "Formatted" },
  { value: "raw", label: "Raw", text: "Raw" },
];

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
        <Alert variant="warning">
          <AlertTriangle />
          <AlertTitle>This content is not valid JSON</AlertTitle>
          <AlertDescription>{parsed.error}</AlertDescription>
        </Alert>
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
        <SegmentedControl label="JSON view" value={mode} onChange={setMode} options={VIEW_MODES} />

        {mode === "tree" && (
          <>
            <Button type="button" variant="outline" size="xs" onClick={() => setExpandToken((t) => t + 1)}>
              <ChevronsUpDown />
              Expand all
            </Button>
            <Button type="button" variant="outline" size="xs" onClick={() => setCollapseToken((t) => t + 1)}>
              <ChevronsDownUp />
              Collapse all
            </Button>

            <SearchInput
              type="search"
              className="w-56"
              // md:text-xs as well as text-xs: ui/input's base is
              // `text-base md:text-sm`, and a bare `text-xs` leaves the
              // breakpoint rule standing on every desktop viewport.
              inputClassName="h-8 text-xs md:text-xs"
              value={query}
              onChange={(e) => {
                setQuery(e.target.value);
                setActiveMatch(0);
              }}
              placeholder="Search keys and values"
              aria-label="Search keys and values"
            />

            {query.trim() !== "" && (
              <div className="flex items-center gap-1 text-xs text-muted-foreground">
                <span aria-live="polite">
                  {matches.length === 0 ? "No matches" : `${activeMatch + 1} of ${matches.length}`}
                </span>
                <Button
                  type="button"
                  variant="outline"
                  size="icon-xs"
                  onClick={() => step(-1)}
                  disabled={matches.length === 0}
                  aria-label="Previous match"
                >
                  <ArrowUp />
                </Button>
                <Button
                  type="button"
                  variant="outline"
                  size="icon-xs"
                  onClick={() => step(1)}
                  disabled={matches.length === 0}
                  aria-label="Next match"
                >
                  <ArrowDown />
                </Button>
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
