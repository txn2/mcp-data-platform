import { useCallback, useEffect, useState } from "react";

/** The query parameter that opens a page's content in the Expand view
 * (#2390), so a link can open an asset straight into it. */
export const EXPANDED_PARAM = "view";
export const EXPANDED_VALUE = "full";

function readExpanded(): boolean {
  return new URLSearchParams(window.location.search).get(EXPANDED_PARAM) === EXPANDED_VALUE;
}

function urlWith(expanded: boolean): string {
  const url = new URL(window.location.href);
  if (expanded) url.searchParams.set(EXPANDED_PARAM, EXPANDED_VALUE);
  else url.searchParams.delete(EXPANDED_PARAM);
  return url.pathname + url.search + url.hash;
}

interface HistoryState {
  expandedView?: boolean;
}

/**
 * Whether the content is shown edge to edge, kept in the address (#2390).
 *
 * Opening it adds a history entry, so the phone's back gesture and the
 * browser's Back close it the way they close any full-screen view; closing it
 * goes back over that entry. A page opened with ?view=full has no entry under
 * it to go back to, so closing it removes the parameter in place. The entry
 * keeps the state the page was opened with, so the page's own Back still
 * knows whether it was reached in-app.
 */
export function useExpandedView(): { expanded: boolean; expand: () => void; close: () => void } {
  const [expanded, setExpanded] = useState(readExpanded);

  useEffect(() => {
    const onPop = () => setExpanded(readExpanded());
    window.addEventListener("popstate", onPop);
    return () => window.removeEventListener("popstate", onPop);
  }, []);

  const expand = useCallback(() => {
    if (readExpanded()) return;
    const state = (window.history.state as Record<string, unknown> | null) ?? {};
    window.history.pushState({ ...state, expandedView: true }, "", urlWith(true));
    setExpanded(true);
  }, []);

  const close = useCallback(() => {
    if ((window.history.state as HistoryState | null)?.expandedView) {
      window.history.back();
      return;
    }
    window.history.replaceState(window.history.state, "", urlWith(false));
    setExpanded(false);
  }, []);

  return { expanded, expand, close };
}
