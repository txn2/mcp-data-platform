import type { Page } from "@playwright/test";

/**
 * A contrast failure: one element of the rendered document whose own text does
 * not stand out from its own ground.
 */
export interface ContrastFailure {
  where: string;
  ratio: number;
  required: number;
  color: string;
  background: string;
  text: string;
}

/**
 * contrastFailures walks every element of the rendered document that carries
 * its own text, resolves the background it actually sits on, and returns the
 * pairs below the WCAG AA threshold for their size.
 *
 * It is a sweep rather than a list of selectors on purpose (#1749). ReDoc is a
 * renderer the portal embeds and does not style: its sources carry literal
 * light-theme colors in a dozen components -- the section labels
 * ("AUTHORIZATIONS:", "QUERY PARAMETERS") are `color: rgba(38, 50, 56, 0.7)`
 * written into the stylesheet, not a theme key -- and none of them is reachable
 * through the theme object ReDoc accepts. A renderer like that is checked over
 * its whole surface or not at all: the page's previous theme test named two
 * selectors, and a third label class shipped invisible on the dark theme
 * because nothing was looking at it.
 *
 * @param page the portal page with the reference already rendered.
 * @param root the container the rendered document is mounted in.
 */
export async function contrastFailures(
  page: Page,
  root = ".redoc-host",
): Promise<ContrastFailure[]> {
  return page.evaluate((hostSelector) => {
    // WCAG 2.1: normal text needs 4.5:1, large text (>=24px, or >=18.66px when
    // bold) needs 3:1.
    const AA_NORMAL = 4.5;
    const AA_LARGE = 3;
    const LARGE_PX = 24;
    const LARGE_BOLD_PX = 18.66;

    type RGBA = [number, number, number, number];

    const parse = (css: string): RGBA | null => {
      const n = css.match(/[\d.]+/g);
      if (!n) return null;
      const [r, g, b, a] = n.map(Number);
      return [r, g, b, a === undefined ? 1 : a];
    };

    /** over composites a translucent color onto an opaque one. */
    const over = (fg: RGBA, bg: RGBA): RGBA => [
      fg[0] * fg[3] + bg[0] * (1 - fg[3]),
      fg[1] * fg[3] + bg[1] * (1 - fg[3]),
      fg[2] * fg[3] + bg[2] * (1 - fg[3]),
      1,
    ];

    /** luminance is WCAG's relative luminance, on sRGB. */
    const luminance = ([r, g, b]: RGBA): number => {
      const channel = (v: number) => {
        const c = v / 255;
        return c <= 0.04045 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4);
      };
      return 0.2126 * channel(r) + 0.7152 * channel(g) + 0.0722 * channel(b);
    };

    const ratio = (a: RGBA, b: RGBA): number => {
      const [l1, l2] = [luminance(a), luminance(b)].sort((x, y) => y - x);
      return (l1 + 0.05) / (l2 + 0.05);
    };

    /** groundOf resolves the opaque background an element's text sits on, by
     * compositing every translucent layer above it down to the page. */
    const groundOf = (el: Element): RGBA => {
      const layers: RGBA[] = [];
      let node: Element | null = el;
      while (node) {
        const bg = parse(getComputedStyle(node).backgroundColor);
        if (bg && bg[3] > 0) {
          layers.push(bg);
          if (bg[3] === 1) break;
        }
        node = node.parentElement;
      }
      // White is the last resort: a document that reaches the root without an
      // opaque background is painted on the browser's own canvas.
      let ground: RGBA = [255, 255, 255, 1];
      for (let i = layers.length - 1; i >= 0; i--)
        ground = over(layers[i], ground);
      return ground;
    };

    /** ownText is the text a node renders itself, rather than through a child.
     * An element that only wraps others is styled by what it contains. */
    const ownText = (el: Element): string =>
      Array.from(el.childNodes)
        .filter((n) => n.nodeType === Node.TEXT_NODE)
        .map((n) => n.textContent ?? "")
        .join("")
        .trim();

    /** painted reports whether an element is actually on the page. */
    const painted = (el: Element, style: CSSStyleDeclaration): boolean => {
      if (style.visibility === "hidden" || style.display === "none")
        return false;
      if (Number(style.opacity) === 0) return false;
      const rect = el.getBoundingClientRect();
      return rect.width > 0 && rect.height > 0;
    };

    /** required is the ratio this element's text has to clear, by its size. */
    const required = (style: CSSStyleDeclaration): number => {
      const size = parseFloat(style.fontSize);
      const weight = Number(style.fontWeight) || 400;
      const large =
        size >= LARGE_PX || (size >= LARGE_BOLD_PX && weight >= 700);
      return large ? AA_LARGE : AA_NORMAL;
    };

    /** describe names an element well enough to find it in the source. */
    const describe = (el: Element): string => {
      const cls = (el.getAttribute("class") ?? "")
        .split(/\s+/)
        .filter(Boolean)
        .slice(0, 2)
        .join(".");
      return cls
        ? `${el.tagName.toLowerCase()}.${cls}`
        : el.tagName.toLowerCase();
    };

    /** check returns this element's failure, or null when it is fine, has no
     * text of its own, or is not painted. */
    const check = (el: Element): ContrastFailure | null => {
      const text = ownText(el);
      if (!text) return null;
      const style = getComputedStyle(el);
      if (!painted(el, style)) return null;
      const fg = parse(style.color);
      if (!fg || fg[3] === 0) return null;

      const ground = groundOf(el);
      const got = ratio(over(fg, ground), ground);
      const need = required(style);
      if (got >= need) return null;
      return {
        where: describe(el),
        ratio: Math.round(got * 100) / 100,
        required: need,
        color: style.color,
        background: `rgb(${ground.slice(0, 3).map(Math.round).join(", ")})`,
        text: text.slice(0, 60),
      };
    };

    const host = document.querySelector(hostSelector);
    if (!host) {
      return [
        {
          where: hostSelector,
          ratio: 0,
          required: AA_NORMAL,
          color: "",
          background: "",
          text: "the document did not render",
        },
      ];
    }

    const failures: ContrastFailure[] = [];
    for (const el of Array.from(host.querySelectorAll("*"))) {
      const failure = check(el);
      if (failure) failures.push(failure);
    }
    return failures;
  }, root);
}

/** report renders the sweep's findings as a failure message. */
export function report(failures: ContrastFailure[]): string {
  return failures
    .map(
      (f) =>
        `  ${f.where}: ${f.ratio}:1 (needs ${f.required}:1) ` +
        `${f.color} on ${f.background} -- ${JSON.stringify(f.text)}`,
    )
    .join("\n");
}
