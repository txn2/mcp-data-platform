import DOMPurify from "dompurify";

/**
 * What a Graphviz drawing is allowed to carry into the page (#2303).
 *
 * Graphviz writes an SVG from the source's attributes: a node's URL, href and
 * target become an <a>, its image an <image>, its tooltip a <title>. The
 * source is whatever its author wrote, so the drawing is cleaned before it is
 * placed in the page: links are unwrapped to their content, anything that
 * loads or runs something is removed, and no attribute that names an address
 * or a handler survives. A tooltip stays, as text.
 */

/** The layout engines a source may name with its layout attribute. */
export const DOT_ENGINES = ["dot", "neato", "fdp", "sfdp", "circo", "twopi", "osage", "patchwork"] as const;
export type DotEngine = (typeof DOT_ENGINES)[number];

const LAYOUT_RE = /\blayout\s*=\s*"?([a-z]+)"?/i;

/**
 * The engine a source names with `layout=`, or dot. Graphviz's library call
 * lays out with the engine it is handed, not the one the graph names, so the
 * viewer reads the attribute itself, as the dot command does.
 */
export function dotEngine(source: string): DotEngine {
  const named = LAYOUT_RE.exec(source)?.[1]?.toLowerCase();
  return (DOT_ENGINES as readonly string[]).includes(named ?? "") ? (named as DotEngine) : "dot";
}

/** Elements that load or run something, removed with their content. */
const REMOVED = new Set(["script", "image", "foreignobject", "use", "style", "iframe", "object", "embed", "animate", "set"]);

/** Attributes that name an address. Every on* handler is removed as well. */
const ADDRESS_ATTRS = new Set(["href", "xlink:href", "target", "xlink:title", "src"]);

/** A paint or filter reference to anything but a fragment of the drawing itself. */
const EXTERNAL_URL_RE = /url\(\s*['"]?\s*(?!#)/i;

/** Graphviz's default face, which the viewer replaces with the page's own. */
const DEFAULT_FONT = "times,serif";
const PAGE_FONT = "ui-sans-serif, system-ui, sans-serif";

/** Graphviz's default ink and paper, which the viewer replaces with the page's own. */
const DEFAULT_INK = new Set(["black", "#000000"]);
const DEFAULT_PAPER = new Set(["white", "#ffffff"]);

/**
 * Parses Graphviz's SVG output and returns the cleaned drawing, or throws when
 * it is not an SVG document.
 *
 * Graphviz's default black ink becomes currentColor and its white page
 * background is removed, so the drawing takes the reader's color scheme; a
 * color the source chose is kept.
 */
export function cleanGraphvizSvg(svgText: string): SVGSVGElement {
  // DOMPurify's SVG profile first, as every other SVG the portal places
  // inline goes through; the rules below are what is particular to a graph.
  const purified = DOMPurify.sanitize(svgText, { USE_PROFILES: { svg: true, svgFilters: true } });
  const doc = new DOMParser().parseFromString(purified, "image/svg+xml");
  const root = doc.documentElement;
  if (root.nodeName.toLowerCase() !== "svg" || doc.getElementsByTagName("parsererror").length > 0) {
    throw new Error("Graphviz did not produce an SVG drawing");
  }
  unwrapLinks(root);
  for (const el of Array.from(root.querySelectorAll("*"))) {
    if (REMOVED.has(el.localName.toLowerCase())) {
      el.remove();
      continue;
    }
    cleanAttributes(el);
    if (el.localName === "text") inkText(el);
  }
  cleanAttributes(root);
  removePageBackground(root);
  return root as unknown as SVGSVGElement;
}

/** Replaces every <a> with its children, innermost first. */
function unwrapLinks(root: Element) {
  const links = Array.from(root.getElementsByTagName("a")).reverse();
  for (const a of links) {
    a.replaceWith(...Array.from(a.childNodes));
  }
}

function cleanAttributes(el: Element) {
  for (const attr of Array.from(el.attributes)) {
    const name = attr.name.toLowerCase();
    if (name.startsWith("on") || ADDRESS_ATTRS.has(name) || EXTERNAL_URL_RE.test(attr.value)) {
      el.removeAttribute(attr.name);
      continue;
    }
    if ((name === "stroke" || name === "fill") && DEFAULT_INK.has(attr.value.toLowerCase())) {
      el.setAttribute(attr.name, "currentColor");
    }
  }
}

/**
 * Text Graphviz writes in its default black carries no fill, and its default
 * face is Times: both take the page's own instead.
 */
function inkText(el: Element) {
  if (!el.hasAttribute("fill")) el.setAttribute("fill", "currentColor");
  if ((el.getAttribute("font-family") ?? "").toLowerCase().replace(/\s/g, "") === DEFAULT_FONT) {
    el.setAttribute("font-family", PAGE_FONT);
  }
}

/** Graphviz paints the page as the first polygon of the top-level graph group. */
function removePageBackground(root: Element) {
  const graph = Array.from(root.children).find((c) => c.localName === "g");
  const page = graph ? Array.from(graph.children).find((c) => c.localName === "polygon") : undefined;
  if (page && DEFAULT_PAPER.has((page.getAttribute("fill") ?? "").toLowerCase())) {
    page.remove();
  }
}
