/**
 * What the viewer knows about a slide deck from its document alone, and the
 * one thing it asks of one (#1769).
 *
 * A deck is an HTML asset that loads the reveal.js runtime the platform serves
 * (#1767); nothing else marks it. The document runs in a sandboxed frame with
 * an opaque origin, so the page that frames it cannot call into it. What it
 * can do is post a message, and the runtime answers messages of the shape
 * below by default (its `postMessage` option): the page asks for the overview
 * that way. A deck's PDF is printed by the platform, not in the browser
 * (#1983, lib/pdfExport).
 */

/** The served runtime's script path, which every deck names (#1767). */
export const RUNTIME_PATH = "/portal/vendor/reveal/reveal.js";

/** Whether the document loads the served slide runtime. */
export function isSlideDeck(content: string): boolean {
  return content.includes(RUNTIME_PATH);
}

/**
 * The message the runtime answers by toggling its overview, in the JSON form
 * its postMessage API reads.
 */
export function overviewMessage(): string {
  return JSON.stringify({ method: "toggleOverview", args: [] });
}
