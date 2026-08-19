import { lazy, Suspense, useState, type ReactNode } from "react";
import { createRoot } from "react-dom/client";
import { ErrorBoundary } from "./components/ErrorBoundary";
import { ContentRenderer } from "./components/renderers/ContentRenderer";
import { resolveRenderer } from "./components/renderers/registry";

// The markdown viewer is behind the same lazy boundary the other families use,
// so the entry chunk this page always loads carries React and the registry and
// nothing family-specific.
const MarkdownRenderer = lazy(() =>
  import("./components/renderers/MarkdownRenderer").then((m) => ({ default: m.MarkdownRenderer })),
);

const ViewerLoading = () => (
  <div className="flex items-center justify-center py-16 text-sm text-muted-foreground">
    Loading viewer...
  </div>
);

/**
 * Shown when the renderer fails to arrive. The page around it still carries
 * the asset's name, description and download link, so this says what is
 * missing rather than replacing the page with an error.
 */
const ViewerFailed = () => (
  <div style={{ padding: "3rem 1rem", textAlign: "center" }}>
    <p style={{ fontSize: "0.875rem", fontWeight: 500 }}>The preview could not be loaded.</p>
    <p style={{ fontSize: "0.875rem", opacity: 0.6, marginTop: "0.25rem" }}>
      Reloading this page may fix it. The download link below still works.
    </p>
  </div>
);

/**
 * Every mount goes through this: the renderers arrive on demand, and a chunk
 * that does not arrive would otherwise take the whole page down with it —
 * including the metadata and download link the reader can still use.
 */
function guarded(children: ReactNode): ReactNode {
  return (
    <ErrorBoundary fallback={<ViewerFailed />}>
      <Suspense fallback={<ViewerLoading />}>{children}</Suspense>
    </ErrorBoundary>
  );
}

function MarkdownWithSourceToggle({ content }: { content: string }) {
  const [showSource, setShowSource] = useState(false);
  return (
    <>
      <div className="flex justify-end mb-2">
        <button
          type="button"
          onClick={() => setShowSource(!showSource)}
          className="inline-flex items-center gap-1.5 rounded-md border px-3 py-1.5 text-sm font-medium hover:bg-accent transition-colors"
        >
          {showSource ? "View Rendered" : "View Markdown"}
        </button>
      </div>
      {showSource
        ? <pre className="rounded-lg border bg-card p-6 text-sm overflow-auto whitespace-pre-wrap">{content}</pre>
        : guarded(<MarkdownRenderer content={content} />)}
    </>
  );
}

function formatBytesSimple(bytes: number): string {
  if (bytes === 0) return "0 B";
  const k = 1024;
  const sizes = ["B", "KB", "MB", "GB"];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return `${parseFloat((bytes / Math.pow(k, i)).toFixed(1))} ${sizes[i]}`;
}

function TooLargeMessage({ sizeBytes, downloadURL, name }: { sizeBytes: number; downloadURL?: string; name?: string }) {
  return (
    <div style={{ display: "flex", flexDirection: "column", alignItems: "center", justifyContent: "center", gap: "1rem", padding: "5rem 1rem", textAlign: "center" }}>
      <svg xmlns="http://www.w3.org/2000/svg" width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" style={{ opacity: 0.5 }}>
        <path d="M15 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7Z"/><path d="M14 2v4a2 2 0 0 0 2 2h4"/><path d="M12 12v6"/><path d="m15 15-3-3-3 3"/>
      </svg>
      <div>
        <p style={{ fontSize: "1.125rem", fontWeight: 500 }}>Asset too large to preview</p>
        <p style={{ fontSize: "0.875rem", opacity: 0.6, marginTop: "0.25rem" }}>
          This file is {formatBytesSimple(sizeBytes)}.
        </p>
      </div>
      {downloadURL && (
        <a
          href={downloadURL}
          download={name || true}
          style={{
            display: "inline-flex", alignItems: "center", gap: "0.5rem",
            padding: "0.5rem 1rem", borderRadius: "0.375rem",
            backgroundColor: "var(--accent, #3b82f6)", color: "#fff",
            fontSize: "0.875rem", fontWeight: 500, textDecoration: "none",
          }}
        >
          <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="7 10 12 15 17 10"/><line x1="12" y1="15" x2="12" y2="3"/></svg>
          Download
        </a>
      )}
    </div>
  );
}

// Read content from embedded JSON (injected by Go template).
//
// `serveFromURL` marks a binary asset the page must load from the raw content
// endpoint: the payload below is a JSON string, which cannot carry arbitrary
// bytes, so images, audio, video and PDFs arrive as a URL and nothing else.
const dataEl = document.getElementById("content-data");
if (dataEl) {
  const { contentType, content, name, tooLarge, sizeBytes, downloadURL, contentURL, serveFromURL } =
    JSON.parse(dataEl.textContent!);
  const root = document.getElementById("content-root");
  if (root) {
    if (tooLarge) {
      createRoot(root).render(<TooLargeMessage sizeBytes={sizeBytes || 0} downloadURL={downloadURL} name={name} />);
    } else {
      const entry = resolveRenderer({
        contentType,
        fileName: name,
        content: serveFromURL ? undefined : content,
      });
      createRoot(root).render(
        entry.kind === "markdown"
          ? <MarkdownWithSourceToggle content={content} />
          : guarded(
              <ContentRenderer
                contentType={contentType}
                content={serveFromURL ? undefined : content}
                fileName={name}
                contentUrl={contentURL || downloadURL}
                sizeBytes={sizeBytes}
              />,
            ),
      );
    }
  }
}

// Expose MarkdownRenderer for pages that need to render multiple markdown blocks
// (e.g., public collection viewer with collection + section descriptions).
// Uses the exact same React component as the single-asset viewer.
type MarkdownHost = Window & {
  renderMarkdown?: (element: HTMLElement, content: string) => void;
  /** Blocks the host page queued before this bundle finished loading. */
  __pendingMarkdown?: [HTMLElement, string][];
};

function renderMarkdown(element: HTMLElement, content: string) {
  createRoot(element).render(guarded(<MarkdownRenderer content={content} bare />));
}

const host = window as MarkdownHost;
host.renderMarkdown = renderMarkdown;

// This bundle is loaded as a module, so it runs after the host page's inline
// script whatever order the tags are in. The collection viewer therefore
// queues its descriptions instead of calling renderMarkdown directly; drain
// what it left.
const pending = host.__pendingMarkdown;
host.__pendingMarkdown = undefined;
if (pending) {
  for (const [element, content] of pending) {
    renderMarkdown(element, content);
  }
}

// Bridge data-theme attribute to .dark class for Tailwind's dark: variant.
// The public viewer template already toggles .dark in its own applyTheme(),
// but this observer is a defensive fallback for any host page that sets
// data-theme without also toggling the class (e.g. third-party embeds).
function syncDarkClass() {
  const dark =
    document.documentElement.getAttribute("data-theme") === "dark";
  document.documentElement.classList.toggle("dark", dark);
}
syncDarkClass();
new MutationObserver(syncDarkClass).observe(document.documentElement, {
  attributes: true,
  attributeFilter: ["data-theme"],
});
