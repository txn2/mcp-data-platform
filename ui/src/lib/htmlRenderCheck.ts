/**
 * Does a reformatted HTML document still render the same text (#1839)?
 *
 * Prettier decides where whitespace is safe to add or collapse from each
 * element's default CSS display, and cannot see the document's own
 * stylesheet. An element the author styled `white-space: pre` (or pre-wrap,
 * pre-line, break-spaces) keeps every space and newline on screen, and
 * Prettier reflows its text anyway. Only a browser knows which elements those
 * are, so the check renders both versions and compares their `innerText`,
 * which is the text as laid out: collapsed where CSS collapses whitespace,
 * kept where it keeps it.
 *
 * Each document is rendered in an offscreen `srcdoc` frame sandboxed with
 * `allow-same-origin` and without `allow-scripts`: no script in the document
 * runs, and the parent may read the rendered text. The comparison covers the
 * static markup. Script bodies are reformatted by a JavaScript printer that
 * keeps their meaning, so the elements a script builds are unchanged.
 *
 * Sharing the portal's origin means a request the frame made would carry the
 * reader's session, so each copy is given a policy that fetches nothing but
 * stylesheets: inline ones and https ones, which is where a white-space rule
 * comes from. Images and fonts change where lines wrap on screen, never the
 * text innerText reports, so blocking them costs the comparison nothing.
 */

/** How long a frame may take to load its stylesheets before it is read anyway. */
const LOAD_TIMEOUT_MS = 5_000;

const CHECK_POLICY =
  '<meta http-equiv="Content-Security-Policy" content="default-src \'none\'; style-src \'unsafe-inline\' https:">';

/**
 * The document with CHECK_POLICY at its head. It goes after a doctype, so the
 * document keeps its rendering mode, and the parser files a meta that comes
 * before <html> under the implied <head>, where a policy is honored.
 */
export function withCheckPolicy(html: string): string {
  const doctype = /^\s*<!doctype[^>]*>/i.exec(html);
  if (!doctype) return CHECK_POLICY + html;
  const end = doctype[0].length;
  return html.slice(0, end) + CHECK_POLICY + html.slice(end);
}

/** True when both documents lay out to the same text. */
export async function rendersSameText(before: string, after: string): Promise<boolean> {
  const [a, b] = await Promise.all([renderedText(before), renderedText(after)]);
  return a === b;
}

function renderedText(html: string): Promise<string> {
  return new Promise((resolve) => {
    const frame = document.createElement("iframe");
    frame.setAttribute("sandbox", "allow-same-origin");
    frame.setAttribute("aria-hidden", "true");
    frame.tabIndex = -1;
    // Offscreen but laid out: innerText reads nothing from a frame that is
    // display:none or visibility:hidden.
    frame.style.cssText =
      "position:fixed;left:-10000px;top:0;width:1024px;height:768px;border:0;opacity:0;pointer-events:none";

    let settled = false;
    const finish = () => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      const text = frame.contentDocument?.body?.innerText ?? "";
      frame.remove();
      resolve(text);
    };
    const timer = setTimeout(finish, LOAD_TIMEOUT_MS);
    frame.addEventListener("load", finish, { once: true });
    frame.srcdoc = withCheckPolicy(html);
    document.body.appendChild(frame);
  });
}
