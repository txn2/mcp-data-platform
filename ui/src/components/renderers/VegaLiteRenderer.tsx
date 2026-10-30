import { useEffect, useRef, useState } from "react";
import type { LoggerInterface } from "vega";
import embed, { type Result, type VisualizationSpec } from "vega-embed";
import { expressionInterpreter } from "vega-interpreter";
import { refsOnlyLoader } from "./vegaLoader";

interface Props {
  content: string;
  /** Draw the chart to fill its container, as a tile does. */
  fit?: boolean;
  /** The color scheme to draw in; the page's own when omitted. */
  dark?: boolean;
  /** Called with "" once the chart is drawn, or with why it is not. */
  onDrawn?: (reason: string) => void;
}

/** Vega's warning level, the level its loader reports a refused load at. */
const WARN = 2;

/**
 * A logger that keeps every load Vega reports failing, so a chart missing its
 * data says why instead of drawing empty axes. Anything else goes to the
 * console as Vega would have sent it.
 */
function collectingLogger(failures: string[]): LoggerInterface {
  let current = WARN;
  const text = (args: unknown[]) => args.map((a) => (a instanceof Error ? a.message : String(a))).join(" ");
  const self: LoggerInterface = {
    level(level?: number) {
      if (level === undefined) return current;
      current = level;
      return self;
    },
    error(...args: unknown[]) {
      failures.push(text(args));
      return self;
    },
    warn(...args: unknown[]) {
      if (String(args[0]).startsWith("Loading failed")) failures.push(text(args));
      else console.warn(...args);
      return self;
    },
    info() {
      return self;
    },
    debug() {
      return self;
    },
  };
  return self;
}

function pageIsDark(): boolean {
  return document.documentElement.classList.contains("dark");
}

/**
 * A Vega-Lite specification, drawn (#2296).
 *
 * Three things keep a spec from doing more than drawing:
 *   - expressions are interpreted (vega-interpreter, `ast: true`), never
 *     compiled with the Function constructor, so the share page's policy needs
 *     no 'unsafe-eval';
 *   - every load goes through refsOnlyLoader, which loads only a declared
 *     reference and follows no link;
 *   - the embed's action menu is off, so nothing posts the spec elsewhere.
 */
export function VegaLiteRenderer({ content, fit, dark, onDrawn }: Props) {
  const host = useRef<HTMLDivElement>(null);
  const [error, setError] = useState<string | null>(null);
  const [scheme, setScheme] = useState(() => dark ?? pageIsDark());

  // Follow the page's scheme when it is toggled, unless the caller chose one.
  useEffect(() => {
    if (dark !== undefined) {
      setScheme(dark);
      return;
    }
    const observer = new MutationObserver(() => setScheme(pageIsDark()));
    observer.observe(document.documentElement, { attributes: true, attributeFilter: ["class"] });
    return () => observer.disconnect();
  }, [dark]);

  useEffect(() => {
    const el = host.current;
    if (!el) return;
    let cancelled = false;
    let result: Result | undefined;
    setError(null);

    let spec: VisualizationSpec;
    try {
      spec = JSON.parse(content) as VisualizationSpec;
    } catch (err) {
      const reason = `the spec is not valid JSON: ${err instanceof Error ? err.message : String(err)}`;
      setError(reason);
      onDrawn?.(`the chart could not be drawn: ${reason}`);
      return;
    }

    const failures: string[] = [];
    embed(el, spec, {
      mode: "vega-lite",
      renderer: "svg",
      actions: false,
      ast: true,
      expr: expressionInterpreter,
      loader: refsOnlyLoader(),
      theme: scheme ? "dark" : undefined,
      config: { background: "transparent" },
      logger: collectingLogger(failures),
    })
      .then((r) => {
        result = r;
        if (cancelled) {
          r.finalize();
          return;
        }
        if (fit) {
          const svg = el.querySelector("svg");
          svg?.setAttribute("width", "100%");
          svg?.setAttribute("height", "100%");
        }
        if (failures.length > 0) {
          const reason = `data could not be loaded: ${failures.join("; ")}`;
          setError(reason);
          onDrawn?.(`the chart could not be drawn: ${reason}`);
          return;
        }
        onDrawn?.("");
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        const reason = err instanceof Error ? err.message : String(err);
        setError(reason);
        onDrawn?.(`the chart could not be drawn: ${reason}`);
      });
    return () => {
      cancelled = true;
      result?.finalize();
    };
  }, [content, fit, scheme, onDrawn]);

  return (
    <>
      {error && (
        <div role="alert" className="rounded-lg border border-destructive/40 bg-destructive/5 p-4 text-sm">
          <p className="font-medium">This chart could not be drawn.</p>
          <pre className="mt-2 whitespace-pre-wrap font-mono text-xs text-muted-foreground">{error}</pre>
        </div>
      )}
      <div
        ref={host}
        data-testid="vegalite-drawing"
        data-feedback-anchorable
        className={fit ? "h-full w-full" : "flex justify-center overflow-auto"}
        style={error ? { display: "none" } : fit ? { width: "100%", height: "100%" } : undefined}
      />
    </>
  );
}
