import { createRoot } from "react-dom/client";
import { ContentRenderer } from "./components/renderers/ContentRenderer";

// Read content from embedded JSON (injected by Go template).
const dataEl = document.getElementById("content-data");
if (dataEl) {
  const { contentType, content } = JSON.parse(dataEl.textContent!);
  const root = document.getElementById("content-root");
  if (root) {
    createRoot(root).render(
      <ContentRenderer contentType={contentType} content={content} />,
    );
  }
}

// Bridge data-theme attribute (public viewer) to .dark class (Tailwind).
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
