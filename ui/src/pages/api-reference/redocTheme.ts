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
//
// Three values are deliberately a step away from the token they come from, and
// each says why on its own field: ReDoc composites some secondary text at 90%
// alpha, and it renders links and the server URL as normal-size body copy,
// where the portal's own token clears AA only at the sizes the portal uses it
// at. The contrast sweep in e2e/interactive/api-reference.spec.ts is what holds
// all of this: it walks every element of the rendered document on both themes
// and fails on any text below WCAG AA for its size (#1749).

/** Palette is the handful of portal tokens ReDoc's theme is built from. */
interface Palette {
  /** --card: the sheet the document is read on. */
  surface: string;
  /** --muted: the recessed fill of a nested schema or an inline code span. */
  fill: string;
  /** --foreground: body copy. */
  text: string;
  /**
   * --muted-foreground, a step darker on the light theme.
   *
   * ReDoc renders type names and hints at 90% alpha over the fill, and the
   * token composited that way reads 4.27:1 -- below AA by a hair. This is the
   * value whose composite clears it.
   */
  muted: string;
  /** --border. */
  border: string;
  /**
   * --primary, lightened on the dark theme.
   *
   * ReDoc sets links and the server URL in it at body size on the document's
   * own ground, where the dark token reads 3.98:1 against the recessed fill.
   */
  primary: string;
  /** The lighter primary ReDoc labels an additional property with, on `fill`. */
  primaryLight: string;
  /**
   * The four response colors. ReDoc paints each response block in its own
   * color at 7% over the page and writes the block's text in the color itself,
   * so both members of the pair move together and both are stated per theme:
   * ReDoc's own amber reads 1.88:1 on its own ground.
   */
  success: string;
  error: string;
  warning: string;
  info: string;
}

const LIGHT: Palette = {
  surface: "#ffffff",
  fill: "#ebf1f7",
  text: "#020817",
  muted: "#4d5a6b",
  border: "#d7dfea",
  primary: "#2563eb",
  primaryLight: "#1d4ed8",
  success: "#15671d",
  error: "#b91c1c",
  warning: "#8a5a00",
  info: "#186c8e",
};

const DARK: Palette = {
  surface: "#131a25",
  fill: "#1e293b",
  text: "#f8fafc",
  muted: "#94a3b8",
  border: "#27303f",
  primary: "#60a5fa",
  primaryLight: "#93c5fd",
  success: "#4ade80",
  error: "#f87171",
  warning: "#fbbf24",
  info: "#38bdf8",
};

/** FONT_STACK is the portal's body font, so the reference reads as part of the
 * product rather than as an embedded document in someone else's typeface. */
const FONT_STACK = '"Inter", system-ui, -apple-system, sans-serif';

/** PANEL is ReDoc's own dark request/response panel, kept in both themes: it
 * is legible on either, and a light panel would put syntax highlighting tuned
 * for a dark ground on a white one. White on it reads 13.2:1. */
const PANEL = "#263238";

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
        primary: { main: p.primary, light: p.primaryLight },
        success: { main: p.success },
        error: { main: p.error },
        warning: { main: p.warning },
        text: { primary: p.text, secondary: p.muted },
        border: { light: p.border, dark: p.border },
        gray: { 50: p.fill, 100: p.border },
        responses: {
          // info and redirect are not derived from a `colors.*` entry the way
          // success and error are: info carries its own literal and redirect
          // reads warning.dark, which polished darkens past legibility on the
          // dark theme. Both are stated.
          info: { color: p.info },
          redirect: { color: p.warning },
        },
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
      rightPanel: {
        backgroundColor: PANEL,
        textColor: "#ffffff",
        servers: {
          // ReDoc writes the base URL into a `#fff` box and the overlay that
          // lists the servers onto `#fafafa`, both literals. On the dark theme
          // that is the page's own body color on white.
          overlay: { backgroundColor: p.surface, textColor: p.text },
          url: { backgroundColor: p.surface },
        },
      },
      fab: { backgroundColor: p.primary, color: p.surface },
      typography: {
        fontFamily: FONT_STACK,
        headings: { fontFamily: FONT_STACK },
        links: { color: p.primary, visited: p.primary, hover: p.primary },
        code: { color: p.text, backgroundColor: p.fill },
      },
    },
  };
}
