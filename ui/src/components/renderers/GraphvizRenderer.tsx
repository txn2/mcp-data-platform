import { useEffect, useRef, useState } from "react";
import { Graphviz } from "@hpcc-js/wasm-graphviz";
import { cleanGraphvizSvg, dotEngine } from "./graphvizSvg";

/** The Graphviz engine, compiled once per page and shared by every drawing. */
let engine: Promise<Graphviz> | undefined;

function loadGraphviz(): Promise<Graphviz> {
  engine ??= Graphviz.load();
  return engine;
}

interface Props {
  content: string;
  /**
   * Draw the graph to fill its container, as a tile does, rather than at its
   * own size.
   */
  fit?: boolean;
  /** Called with "" once the graph is drawn, or with why it is not. */
  onDrawn?: (reason: string) => void;
}

/**
 * A Graphviz DOT source, drawn (#2303).
 *
 * The layout runs in the browser with Graphviz compiled to WebAssembly, laid
 * out by the engine the source names (dot unless it says otherwise). The SVG
 * it writes is cleaned before it is placed in the page (graphvizSvg), and is
 * placed as nodes, never as markup.
 */
export function GraphvizRenderer({ content, fit, onDrawn }: Props) {
  const host = useRef<HTMLDivElement>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    setError(null);
    loadGraphviz()
      .then((gv) => {
        if (cancelled || !host.current) return;
        const svg = cleanGraphvizSvg(gv.layout(content, "svg", dotEngine(content)));
        if (fit) {
          svg.setAttribute("width", "100%");
          svg.setAttribute("height", "100%");
          svg.setAttribute("preserveAspectRatio", "xMidYMid meet");
        } else {
          svg.style.maxWidth = "100%";
          svg.style.height = "auto";
        }
        host.current.replaceChildren(document.importNode(svg, true));
        onDrawn?.("");
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        const reason = err instanceof Error ? err.message : String(err);
        setError(reason);
        onDrawn?.(`the graph could not be drawn: ${reason}`);
      });
    return () => {
      cancelled = true;
    };
  }, [content, fit, onDrawn]);

  if (error) {
    return (
      <div role="alert" className="rounded-lg border border-destructive/40 bg-destructive/5 p-4 text-sm">
        <p className="font-medium">This graph could not be drawn.</p>
        <pre className="mt-2 whitespace-pre-wrap font-mono text-xs text-muted-foreground">{error}</pre>
      </div>
    );
  }
  return (
    <div
      ref={host}
      data-testid="graphviz-drawing"
      data-feedback-anchorable
      className={fit ? undefined : "flex justify-center overflow-auto text-foreground"}
      style={fit ? { width: "100%", height: "100%" } : undefined}
    />
  );
}
