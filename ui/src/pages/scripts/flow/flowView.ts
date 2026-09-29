// Which picture the Flow tab draws (#1972), kept per viewer in browser storage
// and in the address, so a copied link opens on the view it was copied from.

export type FlowView = "structure" | "calls" | "timeline";

export const FLOW_VIEWS: { value: FlowView; label: string }[] = [
  { value: "structure", label: "Structure" },
  { value: "calls", label: "Calls" },
  { value: "timeline", label: "Timeline" },
];

const PARAM = "view";
const STORAGE_KEY = "portal.flow.view";

function isView(v: string | null | undefined): v is FlowView {
  return v === "structure" || v === "calls" || v === "timeline";
}

// initialView is the address's view, else the one this viewer last chose,
// else Structure.
export function initialView(): FlowView {
  try {
    const fromUrl = new URLSearchParams(window.location.search).get(PARAM);
    if (isView(fromUrl)) return fromUrl;
    const stored = window.localStorage.getItem(STORAGE_KEY);
    if (isView(stored)) return stored;
  } catch {
    // Storage can be unavailable (a private window); the default stands.
  }
  return "structure";
}

// rememberView records a choice in the address, without a navigation, and in
// browser storage.
export function rememberView(view: FlowView): void {
  try {
    const url = new URL(window.location.href);
    url.searchParams.set(PARAM, view);
    window.history.replaceState(window.history.state, "", url.toString());
    window.localStorage.setItem(STORAGE_KEY, view);
  } catch {
    // Neither is required for the view to change.
  }
}
