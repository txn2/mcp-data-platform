/**
 * The light rendition every printed document is given (#1772).
 *
 * A deck is written for a room with the lights off: light text on a dark
 * background. Paper is white, and a browser prints backgrounds only when the
 * reader has turned "Background graphics" on, which is not the default -- so a
 * dark deck printed as it stands is light text on white, with headings and
 * bullets near-invisible on the page and in the saved PDF.
 *
 * So the print document is always rendered light, whatever the document's own
 * theme: the page is white, and every colour that would be unreadable on white
 * is moved to the other side of the scale keeping its hue. Ink that is already
 * dark, and a document that is already light, come out exactly as they are.
 *
 * The rendition runs inside the print document, a sandboxed frame with an
 * opaque origin that the page cannot reach into, so the function below is
 * serialized into that document's own script rather than called across the
 * boundary (lib/deck, PRINT_STEP). That is the one constraint on it:
 * `lightRendition` must not name anything outside itself, because only its own
 * text is carried over.
 */

/** A colour as its three channels, 0..255. */
type Channels = [number, number, number];

/**
 * Render a document for white paper: white page, readable ink, pale surfaces.
 *
 * Self-contained by construction -- see the note above. It is called once, on
 * the document that is about to be printed, after whatever laid it out.
 */
export function lightRendition(doc: Document): void {
  const view = doc.defaultView;
  if (!view) return;

  // The surfaces the paper itself stands for. They are white rather than a
  // tint of the document's own colour, and they are the one thing the pass
  // below leaves to the stylesheet.
  const SURFACES =
    "html, body, .reveal-viewport, .pdf-page, .reveal .slides, .reveal section, .slide-background";
  // Under this luminance a colour already reaches 4.5:1 against white and is
  // left alone; above it, it is ink that has to move.
  const INK_LIMIT = 0.183;
  // Where moved ink lands: the range it is spread across, brightest source to
  // darkest ink, so the document's own hierarchy survives the flip. The
  // ceiling is the luminance that reads at 4.5:1 on white, the floor is a
  // near-black.
  const INK_FLOOR = 0.02;
  const INK_CEILING = 0.18;
  // A fill darker than this is repainted as a pale tint of itself; a mid-tone
  // accent is visible on white and is left as the author wrote it.
  const SURFACE_LIMIT = 0.5;
  // How much of a dark fill survives in its tint.
  const TINT = 0.1;

  /** The channels of a computed colour, or null for one this cannot read. */
  function channels(value: string): Channels | null {
    const inside = /^rgba?\(([^)]+)\)$/.exec(value.trim());
    if (!inside?.[1]) return null;
    const parts = inside[1].split(/[\s,/]+/).filter(Boolean).map(Number);
    const [red, green, blue, alpha] = parts;
    if (red === undefined || green === undefined || blue === undefined) return null;
    if (!Number.isFinite(red + green + blue) || alpha === 0) return null;
    return [red, green, blue];
  }

  /** One channel, 0..255, as the linear-light value luminance is summed from. */
  function toLinear(channel: number): number {
    const unit = channel / 255;
    return unit <= 0.04045 ? unit / 12.92 : Math.pow((unit + 0.055) / 1.055, 2.4);
  }

  /** One linear-light value back to a channel, 0..255. */
  function toChannel(linear: number): number {
    const held = Math.min(Math.max(linear, 0), 1);
    const unit = held <= 0.0031308 ? held * 12.92 : 1.055 * Math.pow(held, 1 / 2.4) - 0.055;
    return Math.round(Math.min(Math.max(unit, 0), 1) * 255);
  }

  /** Relative luminance, which is what "readable on white" is measured in. */
  function luminance([red, green, blue]: Channels): number {
    return 0.2126 * toLinear(red) + 0.7152 * toLinear(green) + 0.0722 * toLinear(blue);
  }

  function css([red, green, blue]: Channels): string {
    return `rgb(${red}, ${green}, ${blue})`;
  }

  /**
   * The same colour at a given luminance. The channels are scaled in linear
   * light, so what comes out is the hue the author wrote, darker.
   */
  function atLuminance(rgb: Channels, target: number, from: number): string {
    const scale = target / from;
    const [red, green, blue] = rgb;
    return css([
      toChannel(toLinear(red) * scale),
      toChannel(toLinear(green) * scale),
      toChannel(toLinear(blue) * scale),
    ]);
  }

  /** Ink for white paper: what is too light to read moves down, nothing else. */
  function ink(value: string): string | null {
    const rgb = channels(value);
    if (!rgb) return null;
    const lit = luminance(rgb);
    if (lit <= INK_LIMIT) return null;
    const across = (lit - INK_LIMIT) / (1 - INK_LIMIT);
    return atLuminance(rgb, INK_CEILING - (INK_CEILING - INK_FLOOR) * across, lit);
  }

  /** A surface for white paper: a dark fill becomes a pale tint of itself. */
  function surface(value: string): string | null {
    const rgb = channels(value);
    if (!rgb || luminance(rgb) >= SURFACE_LIMIT) return null;
    const [red, green, blue] = rgb;
    const pale = (channel: number): number => Math.round(255 - (255 - channel) * TINT);
    return css([pale(red), pale(green), pale(blue)]);
  }

  function paint(el: Element, property: string, next: string | null): void {
    if (next) (el as unknown as ElementCSSInlineStyle).style.setProperty(property, next, "important");
  }

  const style = doc.createElement("style");
  style.textContent =
    `${SURFACES} { background: #fff !important; background-image: none !important; }` +
    `html { color-scheme: light; -webkit-print-color-adjust: exact; print-color-adjust: exact; }` +
    `.reveal .backgrounds { display: none !important; }`;
  (doc.head ?? doc.documentElement).appendChild(style);

  for (const el of doc.querySelectorAll("*")) {
    const computed = view.getComputedStyle(el);
    paint(el, "color", ink(computed.color));
    paint(el, "stroke", ink(computed.stroke));
    if (!el.matches(SURFACES)) {
      paint(el, "background-color", surface(computed.backgroundColor));
    }
    // An SVG glyph is ink; every other shape is a surface behind one.
    const glyph = /^(text|tspan|textPath)$/i.test(el.tagName);
    paint(el, "fill", glyph ? ink(computed.fill) : surface(computed.fill));
  }
}
