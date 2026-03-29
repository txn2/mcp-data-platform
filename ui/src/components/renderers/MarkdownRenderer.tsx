import { useEffect, useRef, useId, useCallback, useState } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import type { Components } from "react-markdown";
import mermaid from "mermaid";

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
          ref.current.innerHTML = svg;
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
          if (ref.current) ref.current.innerHTML = svg;
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

export function MarkdownRenderer({ content }: { content: string }) {
  const components: Components = {
    code: useCallback(
      // react-markdown passes `node` (hast AST) — destructure it out so it
      // doesn't leak into the DOM as an invalid attribute.
      ({ className, children, node: _node, ...rest }: // eslint-disable-line @typescript-eslint/no-unused-vars
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
      ({ node: _node, ...rest }: React.ComponentProps<"pre"> & { node?: unknown }) => ( // eslint-disable-line @typescript-eslint/no-unused-vars
        <pre {...rest} />
      ),
      [],
    ),
  };

  return (
    <div className="prose prose-sm max-w-none dark:prose-invert rounded-lg border bg-card p-6">
      <ReactMarkdown remarkPlugins={[remarkGfm]} components={components}>
        {content}
      </ReactMarkdown>
    </div>
  );
}
