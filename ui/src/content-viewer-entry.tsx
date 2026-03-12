import { createRoot } from "react-dom/client";
import { ContentRenderer } from "./components/renderers/ContentRenderer";

// Read content from embedded JSON (injected by Go template).
const dataEl = document.getElementById("content-data");
if (dataEl) {
  const { contentType, content, name } = JSON.parse(dataEl.textContent!);
  const root = document.getElementById("content-root");
  if (root) {
    createRoot(root).render(
      <ContentRenderer contentType={contentType} content={content} fileName={name} />,
    );
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
