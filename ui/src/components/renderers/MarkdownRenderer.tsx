import { useEffect, useRef, useId, useCallback, useState } from "react";
import ReactMarkdown, { defaultUrlTransform } from "react-markdown";
import remarkGfm from "remark-gfm";
import type { Components } from "react-markdown";
import mermaid from "mermaid";
import DOMPurify from "dompurify";
import { EntityChip } from "@/components/knowledge/EntityChip";
import { isRefUrn, type ResolvedRef } from "@/lib/entityRefs";

// DOMPurify's default FORBID_CONTENTS strips the children of `foreignObject`
// (among others). We reuse that default list but drop `foreignobject` so the
// HTML label markup mermaid places inside it survives. Everything else in the
// list (script, style, iframe, ...) is still content-stripped.
const MERMAID_FORBID_CONTENTS = [
  "annotation-xml", "audio", "colgroup", "desc", "head", "iframe", "math", "mi",
  "mn", "mo", "ms", "mtext", "noembed", "noframes", "noscript", "plaintext",
  "script", "style", "svg", "template", "thead", "title", "video", "xmp",
];

/**
 * Sanitize a mermaid-rendered SVG before injecting it into the DOM.
 *
 * Mermaid v11 renders node labels with `htmlLabels: true` (the default): the
 * label text is an HTML `<span>`/`<div>` living inside an SVG `<foreignObject>`.
 * DOMPurify's `svg`-only profile dropped that HTML, so node labels rendered
 * invisible while subgraph/cluster titles (emitted as SVG `<text>`) survived
 * (issue #521). Three settings are required to keep the labels while staying
 * safe:
 *   - `USE_PROFILES.html` so the `<div>`/`<span>`/`<p>` label tags are allowed.
 *   - `ADD_TAGS: ['foreignObject']` because the svg profile disallows it.
 *   - `HTML_INTEGRATION_POINTS: { foreignobject: true }` so DOMPurify's
 *     namespace check treats foreignObject as an HTML integration point and
 *     keeps its xhtml children (it only allows `annotation-xml` by default).
 * Plus `MERMAID_FORBID_CONTENTS` so foreignObject contents are not stripped.
 * Scripts and inline event handlers (onclick/onerror/...) are still removed.
 */
export function sanitizeMermaidSvg(svg: string): string {
  return DOMPurify.sanitize(svg, {
    USE_PROFILES: { svg: true, svgFilters: true, html: true },
    ADD_TAGS: ["foreignObject"],
    FORBID_CONTENTS: MERMAID_FORBID_CONTENTS,
    HTML_INTEGRATION_POINTS: { foreignobject: true, "annotation-xml": true },
  });
}

/** Detect whether the document is currently in dark mode. */
function isDark(): boolean {
  return document.documentElement.classList.contains("dark");
}

/** Re-initialize mermaid with the appropriate theme. */
function initMermaid() {
  mermaid.initialize({
    startOnLoad: false,
    theme: isDark() ? "dark" : "default",
    fontFamily: "ui-sans-serif, system-ui, sans-serif",
    themeVariables: isDark()
      ? {
          primaryColor: "#3b82f6",
          primaryTextColor: "#f1f5f9",
          primaryBorderColor: "#475569",
          lineColor: "#64748b",
          secondaryColor: "#1e293b",
          tertiaryColor: "#0f172a",
          background: "#0f172a",
          mainBkg: "#1e293b",
          nodeBorder: "#475569",
          clusterBkg: "#1e293b",
          clusterBorder: "#334155",
          titleColor: "#e2e8f0",
          edgeLabelBackground: "#1e293b",
          noteBkgColor: "#1e293b",
          noteTextColor: "#e2e8f0",
          noteBorderColor: "#475569",
        }
      : {
          primaryColor: "#3b82f6",
          primaryTextColor: "#1e293b",
          primaryBorderColor: "#cbd5e1",
          lineColor: "#94a3b8",
          secondaryColor: "#f1f5f9",
          tertiaryColor: "#e2e8f0",
          background: "#ffffff",
          mainBkg: "#f8fafc",
          nodeBorder: "#cbd5e1",
          clusterBkg: "#f8fafc",
          clusterBorder: "#e2e8f0",
          titleColor: "#1e293b",
          edgeLabelBackground: "#ffffff",
          noteBkgColor: "#f8fafc",
          noteTextColor: "#1e293b",
          noteBorderColor: "#cbd5e1",
        },
  });
}

initMermaid();

function MermaidBlock({ content }: { content: string }) {
  const ref = useRef<HTMLDivElement>(null);
  const id = useId().replace(/:/g, "m");
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;

    // Re-init with current theme before each render
    initMermaid();

    mermaid
      .render(id, content)
      .then(({ svg }) => {
        if (!cancelled && ref.current) {
          ref.current.innerHTML = sanitizeMermaidSvg(svg);
          setError(null);
        }
      })
      .catch((err) => {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : "Failed to render diagram");
        }
      });

    return () => {
      cancelled = true;
    };
  }, [id, content]);

  // Listen for theme changes and re-render
  useEffect(() => {
    const observer = new MutationObserver(() => {
      initMermaid();
      mermaid
        .render(id + "t", content)
        .then(({ svg }) => {
          if (ref.current) ref.current.innerHTML = sanitizeMermaidSvg(svg);
        })
        .catch(() => {});
    });

    observer.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ["class"],
    });

    return () => observer.disconnect();
  }, [id, content]);

  if (error) {
    return (
      <div className="my-4 rounded-lg border border-red-200 bg-red-50 p-4 text-sm text-red-700 dark:border-red-900 dark:bg-red-950 dark:text-red-300">
        <p className="font-medium">Diagram Error</p>
        <pre className="mt-1 text-xs opacity-75">{error}</pre>
      </div>
    );
  }

  return (
    <div className="my-4 not-prose">
      <div
        ref={ref}
        className="flex justify-center rounded-lg border border-border bg-muted/30 p-6 overflow-x-auto"
      />
    </div>
  );
}

export function MarkdownRenderer({
  content,
  bare,
  refs,
}: {
  content: string | null | undefined;
  bare?: boolean;
  refs?: Map<string, ResolvedRef>;
}) {
  const components: Components = {
    // Render mcp:/urn: links as entity chips; ordinary links are unchanged.
    a: useCallback(
      ({ href, children, node: _node, ...rest }:
        React.ComponentProps<"a"> & { node?: unknown }) => {
        if (isRefUrn(href)) {
          return <EntityChip urn={href as string} resolved={refs?.get(href as string)} />;
        }
        return (
          <a href={href} {...rest}>
            {children}
          </a>
        );
      },
      [refs],
    ),
    code: useCallback(
      // react-markdown passes `node` (hast AST) — destructure it out so it
      // doesn't leak into the DOM as an invalid attribute.
      ({ className, children, node: _node, ...rest }:
        React.ComponentProps<"code"> & { node?: unknown }) => {
        const match = /language-(\w+)/.exec(className || "");
        const lang = match?.[1];
        const text = String(children).replace(/\n$/, "");

        if (lang === "mermaid") {
          return <MermaidBlock content={text} />;
        }

        return (
          <code className={className} {...rest}>
            {children}
          </code>
        );
      },
      [],
    ),
    // Strip the `node` prop from <pre> as well to prevent DOM warnings.
    pre: useCallback(
      ({ node: _node, ...rest }: React.ComponentProps<"pre"> & { node?: unknown }) => (
        <pre {...rest} />
      ),
      [],
    ),
  };

  if (!content) return null;

  return (
    <div
      data-feedback-anchorable
      className={`prose prose-sm max-w-none dark:prose-invert [&>*:first-child]:mt-0 [&>*:last-child]:mb-0 ${bare ? "" : "rounded-lg border bg-card p-6"}`}
    >
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        components={components}
        // Preserve entity-reference URNs (mcp:/urn:) so the `a` override can chip
        // them; everything else keeps react-markdown's default URL sanitization.
        urlTransform={(url) => (isRefUrn(url) ? url : defaultUrlTransform(url))}
      >
        {content}
      </ReactMarkdown>
    </div>
  );
}
