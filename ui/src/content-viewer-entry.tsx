import { useState } from "react";
import { createRoot } from "react-dom/client";
import { ContentRenderer } from "./components/renderers/ContentRenderer";
import { MarkdownRenderer } from "./components/renderers/MarkdownRenderer";

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
        : <MarkdownRenderer content={content} />}
    </>
  );
}

// Read content from embedded JSON (injected by Go template).
const dataEl = document.getElementById("content-data");
if (dataEl) {
  const { contentType, content, name } = JSON.parse(dataEl.textContent!);
  const root = document.getElementById("content-root");
  if (root) {
    const ct = (contentType as string).toLowerCase();
    const isMarkdown = ct.includes("markdown");
    createRoot(root).render(
      isMarkdown
        ? <MarkdownWithSourceToggle content={content} />
        : <ContentRenderer contentType={contentType} content={content} fileName={name} />,
    );
  }
}

// Expose MarkdownRenderer for pages that need to render multiple markdown blocks
// (e.g., public collection viewer with collection + section descriptions).
// Uses the exact same React component as the single-asset viewer.
(window as any).renderMarkdown = function(element: HTMLElement, content: string) {
  createRoot(element).render(<MarkdownRenderer content={content} bare />);
};

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
