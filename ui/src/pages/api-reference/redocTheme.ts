import type { RedocRawOptions } from "redoc";

// What the API Reference page hands ReDoc.
//
// ReDoc carries its own light theme and knows nothing about the portal's, so a
// reader on the dark theme would be given near-black text on a near-black page.
//
// The two palettes below are the portal's own tokens (ui/src/theme.css) written
// out as hex rather than referred to as CSS variables. ReDoc resolves a theme
// once, in JavaScript, deriving its hover, border and heading shades with
// `polished`, and polished throws on anything it cannot parse to a color --
// `var(--background)` included. So the values have to be concrete here, and a
// change to a token in theme.css is a change to make in both places.

/** Palette is the handful of portal tokens ReDoc's theme is built from. */
interface Palette {
  /** --card: the sheet the document is read on. */
  surface: string;
  /** --muted: the recessed fill of a nested schema or an inline code span. */
  fill: string;
  /** --foreground: body copy. */
  text: string;
  /** --muted-foreground: type names, hints, the menu's inactive arrows. */
  muted: string;
  /** --border. */
  border: string;
  /** --primary: links, the active menu item. */
  primary: string;
}

const LIGHT: Palette = {
  surface: "#ffffff",
  fill: "#ebf1f7",
  text: "#020817",
  muted: "#5e6d82",
  border: "#d7dfea",
  primary: "#2563eb",
};

const DARK: Palette = {
  surface: "#131a25",
  fill: "#1e293b",
  text: "#f8fafc",
  muted: "#94a3b8",
  border: "#27303f",
  primary: "#3b82f6",
};

/** FONT_STACK is the portal's body font, so the reference reads as part of the
 * product rather than as an embedded document in someone else's typeface. */
const FONT_STACK = '"Inter", system-ui, -apple-system, sans-serif';

/**
 * redocOptions is the whole of the page's configuration.
 *
 * `nativeScrollbars` puts the menu on the browser's own scrollbar rather than
 * ReDoc's simulated one, which measures the window it is not scrolling inside
 * the portal shell. Nothing else is turned off: ReDoc's floating button is the
 * only way to open the tag menu at phone width, so hiding it would leave a
 * phone reader with the document and no way to navigate it.
 */
export function redocOptions(dark: boolean): RedocRawOptions {
  const p = dark ? DARK : LIGHT;
  return {
    nativeScrollbars: true,
    theme: {
      colors: {
        primary: { main: p.primary },
        text: { primary: p.text, secondary: p.muted },
        border: { light: p.border, dark: p.border },
        gray: { 50: p.fill, 100: p.border },
      },
      schema: {
        linesColor: p.border,
        typeNameColor: p.muted,
        typeTitleColor: p.text,
        nestedBackground: p.fill,
        arrow: { color: p.muted },
      },
      sidebar: {
        backgroundColor: p.surface,
        textColor: p.text,
        activeTextColor: p.primary,
        arrow: { color: p.muted },
      },
      typography: {
        fontFamily: FONT_STACK,
        headings: { fontFamily: FONT_STACK },
        links: { color: p.primary, visited: p.primary, hover: p.primary },
        code: { color: p.text, backgroundColor: p.fill },
      },
      // The request/response samples keep ReDoc's own dark panel in both
      // themes. It is legible on either, and a light panel would put syntax
      // highlighting tuned for a dark ground on a white one.
    },
  };
}
