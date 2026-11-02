import DOMPurify from "dompurify";

/**
 * What a D2 drawing is allowed to carry into the page (#2302).
 *
 * The platform renders a D2 source to SVG on the server (internal/d2render);
 * the source passed a save check that refuses imports and any URL but a
 * managed resource's. The drawing is still cleaned before it is placed in the
 * page, as every SVG the portal places inline is: links are unwrapped, nothing
 * that runs survives, an <image> stays only when it names the platform's
 * reference route (an icon declared as a reference, rewritten when it was
 * served) and is pointed at the page's own origin, and a stylesheet or
 * attribute naming any other address loses it. D2's own stylesheet, which
 * scopes its classes to the diagram and carries its fonts as data: URLs, and
 * the HTML a markdown label is drawn with stay.
 */

/** Elements that run or frame something, removed with their content. */
const REMOVED = new Set(["script", "iframe", "object", "embed", "animate", "set", "use"]);

/** The reference route a declared icon is served from (assetrefs.PathPrefix). */
const REFS_PATH = /^\/portal\/refs\/[^\s?#]+$/;

/** A url() to anything but a fragment of the drawing or an inline data: URI. */
const EXTERNAL_URL_RE = /url\(\s*['"]?\s*(?!#|data:)[^)]*\)/gi;
/** The same, as a test that keeps no state between calls. */
const HAS_EXTERNAL_URL = /url\(\s*['"]?\s*(?!#|data:)/i;

/** Attributes that name an address. Every on* handler is removed as well. */
const ADDRESS_ATTRS = new Set(["href", "xlink:href", "target", "src", "action", "formaction"]);

/**
 * Parses a D2 drawing and returns it cleaned, or throws when it is not an SVG
 * document. origin is the page's own, which a reference-route icon is pointed
 * at whatever host the platform rewrote it against.
 */
export function cleanD2Svg(svgText: string, origin: string): SVGSVGElement {
  // The sanitized DOM is taken as it is: a label's HTML serializes as HTML,
  // which an XML parse of the string would refuse.
  const purified = DOMPurify.sanitize(svgText, {
    USE_PROFILES: { svg: true, svgFilters: true, html: true },
    ADD_TAGS: ["style", "foreignObject"],
    // A markdown label is HTML inside a foreignObject; without this DOMPurify
    // drops it whole. The HTML is still sanitized like any other.
    HTML_INTEGRATION_POINTS: { foreignobject: true },
    RETURN_DOM_FRAGMENT: true,
  } as Parameters<typeof DOMPurify.sanitize>[1]) as unknown as DocumentFragment;
  const root = purified.firstElementChild;
  if (!root || root.localName.toLowerCase() !== "svg" || purified.children.length !== 1) {
    throw new Error("the platform did not return an SVG drawing");
  }
  for (const a of Array.from(root.getElementsByTagName("a")).reverse()) a.replaceWith(...Array.from(a.childNodes));
  for (const el of [root, ...Array.from(root.querySelectorAll("*"))]) cleanElement(el, origin);
  return root as unknown as SVGSVGElement;
}

/** Cleans one element in place, removing it when nothing of it may stay. */
function cleanElement(el: Element, origin: string) {
  const name = el.localName.toLowerCase();
  if (REMOVED.has(name)) {
    el.remove();
    return;
  }
  if (name === "image") {
    const href = referenceHref(el.getAttribute("href") ?? el.getAttribute("xlink:href") ?? "", origin);
    if (!href) {
      el.remove();
      return;
    }
    cleanAttributes(el);
    el.setAttribute("href", href);
    return;
  }
  if (name === "style") {
    // D2 writes its own stylesheet in the drawing; a <style> inside a
    // label's HTML (foreignObject) would restyle the portal page around it.
    if (el.closest("foreignObject")) {
      el.remove();
      return;
    }
    el.textContent = (el.textContent ?? "").replace(EXTERNAL_URL_RE, "none");
  }
  cleanAttributes(el);
}

/** The reference-route address an icon names, on origin, or "" for any other. */
function referenceHref(href: string, origin: string): string {
  try {
    const url = new URL(href, origin);
    if ((url.protocol === "https:" || url.protocol === "http:") && REFS_PATH.test(url.pathname) && !url.search) {
      return origin + url.pathname;
    }
  } catch {
    // An address that does not parse names nothing.
  }
  return "";
}

function cleanAttributes(el: Element) {
  for (const attr of Array.from(el.attributes)) {
    const name = attr.name.toLowerCase();
    if (name.startsWith("on") || ADDRESS_ATTRS.has(name) || HAS_EXTERNAL_URL.test(attr.value)) {
      el.removeAttribute(attr.name);
    }
  }
}

/** The address the drawing is read from: the content route asking for it. */
export function drawingURL(contentUrl: string, dark: boolean): string {
  const sep = contentUrl.includes("?") ? "&" : "?";
  return `${contentUrl}${sep}render=svg${dark ? "&theme=dark" : ""}`;
}
