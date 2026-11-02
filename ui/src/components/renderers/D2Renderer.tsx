import { useEffect, useRef, useState } from "react";
import { cleanD2Svg, drawingURL } from "./d2Svg";

interface Props {
  /** The asset's content route; the drawing is read from it with ?render=svg. */
  contentUrl?: string;
  /** The source, which keys a fresh drawing when it is saved anew. */
  content: string;
  /** Draw to fill the container rather than at the drawing's own size. */
  fit?: boolean;
  /** Called with "" once the drawing is placed, or with why it is not. */
  onDrawn?: (reason: string) => void;
}

/** Whether the page is drawn in its dark scheme. */
function pageIsDark(): boolean {
  return typeof document !== "undefined" && document.documentElement.classList.contains("dark");
}

/**
 * A D2 diagram, drawn (#2302).
 *
 * The platform renders the saved source to SVG (D2's browser build cannot run
 * under the share viewer's policy), in the page's light or dark scheme, and
 * the drawing is cleaned (d2Svg) and placed as nodes, never as markup. It is
 * the saved source's drawing: an edit is drawn once it is saved.
 */
export function D2Renderer({ contentUrl, content, fit, onDrawn }: Props) {
  const host = useRef<HTMLDivElement>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!contentUrl) {
      setError("this diagram has no address to be drawn from");
      onDrawn?.("the diagram has no address to be drawn from");
      return;
    }
    let cancelled = false;
    setError(null);
    fetch(drawingURL(contentUrl, pageIsDark()), { credentials: "same-origin" })
      .then(async (res) => {
        const text = await res.text();
        if (!res.ok) {
          let detail = text;
          try {
            detail = (JSON.parse(text) as { detail?: string }).detail ?? text;
          } catch {
            // A body that is not a problem document is shown as it is.
          }
          throw new Error(detail || `the platform answered ${res.status}`);
        }
        return text;
      })
      .then((text) => {
        if (cancelled || !host.current) return;
        const svg = cleanD2Svg(text, window.location.origin);
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
        onDrawn?.(`the diagram could not be drawn: ${reason}`);
      });
    return () => {
      cancelled = true;
    };
  }, [contentUrl, content, fit, onDrawn]);

  if (error) {
    return (
      <div role="alert" className="rounded-lg border border-destructive/40 bg-destructive/5 p-4 text-sm">
        <p className="font-medium">This diagram could not be drawn.</p>
        <pre className="mt-2 whitespace-pre-wrap font-mono text-xs text-muted-foreground">{error}</pre>
      </div>
    );
  }
  return (
    <div
      ref={host}
      data-testid="d2-drawing"
      data-feedback-anchorable
      className={fit ? undefined : "flex justify-center overflow-auto"}
      style={fit ? { width: "100%", height: "100%" } : undefined}
    />
  );
}
