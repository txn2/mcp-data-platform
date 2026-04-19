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

function formatBytesSimple(bytes: number): string {
  if (bytes === 0) return "0 B";
  const k = 1024;
  const sizes = ["B", "KB", "MB", "GB"];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return `${parseFloat((bytes / Math.pow(k, i)).toFixed(1))} ${sizes[i]}`;
}

function TooLargeMessage({ sizeBytes }: { sizeBytes: number }) {
  return (
    <div style={{ display: "flex", flexDirection: "column", alignItems: "center", justifyContent: "center", gap: "1rem", padding: "5rem 1rem", textAlign: "center" }}>
      <svg xmlns="http://www.w3.org/2000/svg" width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" style={{ opacity: 0.5 }}>
        <path d="M15 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7Z"/><path d="M14 2v4a2 2 0 0 0 2 2h4"/><path d="M12 12v6"/><path d="m15 15-3-3-3 3"/>
      </svg>
      <div>
        <p style={{ fontSize: "1.125rem", fontWeight: 500 }}>Asset too large to preview</p>
        <p style={{ fontSize: "0.875rem", opacity: 0.6, marginTop: "0.25rem" }}>
          This file is {formatBytesSimple(sizeBytes)}. Use the download button to save it locally.
        </p>
      </div>
    </div>
  );
}

// Read content from embedded JSON (injected by Go template).
const dataEl = document.getElementById("content-data");
if (dataEl) {
  const { contentType, content, name, tooLarge, sizeBytes } = JSON.parse(dataEl.textContent!);
  const root = document.getElementById("content-root");
  if (root) {
    if (tooLarge) {
      createRoot(root).render(<TooLargeMessage sizeBytes={sizeBytes || 0} />);
    } else {
      const ct = (contentType as string).toLowerCase();
      const isMarkdown = ct.includes("markdown");
      createRoot(root).render(
        isMarkdown
          ? <MarkdownWithSourceToggle content={content} />
          : <ContentRenderer contentType={contentType} content={content} fileName={name} />,
      );
    }
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
