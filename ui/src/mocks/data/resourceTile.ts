import { captureFamily } from "../../lib/thumbnailSupport";
import { resourceImageBytes } from "./resourceImages";

// The tile a fixture resource serves, drawn from the file's own content.
//
// The real capturer rasterizes the rendered document in the browser
// (components/thumbnail/DomThumbnailBody), and the fixtures settle their
// captures so no page under test does that work on the main thread. Settling
// them left every resource claiming a tile that had no bytes behind it, so the
// library rendered as a grid of file-type icons and shipped that way in the
// documentation (#1619).
//
// This draws the head of the document instead: the same families the capturer
// distinguishes, in the same two schemes, at a size a tile is shown at. It is a
// picture of the file rather than a picture somebody drew of the file, so a
// fixture added later carries a tile without anyone drawing one.

/**
 * Tile geometry, taken from the capturer's own page: it renders the document
 * into a THUMB_WIDTH x THUMB_HEIGHT box at 12px with 1.6 line height and 16px
 * of padding (components/ThumbnailGenerator), and rasterizes that. Drawing at
 * the same numbers is what makes the fixture tile the size a real one is
 * rather than a page of text too small to read at tile scale.
 */
const WIDTH = 400;
const HEIGHT = 300;
const PAD = 16;
const BASE = 12;
const LINE = Math.round(BASE * 1.6);
const MAX_LINES = Math.floor((HEIGHT - PAD * 2) / LINE);

/**
 * The colors each scheme is drawn in. They are the capturer's own tokens
 * (components/thumbnail/schemes.ts), so a fixture tile and a real capture read
 * as the same document.
 */
const SCHEMES = {
  light: { bg: "#ffffff", fg: "#111827", muted: "#6b7280", rule: "#d1d5db", head: "#f1f5f9" },
  dark: { bg: "#131a25", fg: "#f8fafc", muted: "#94a3b8", rule: "#334155", head: "#1e293b" },
} as const;

const SANS = "ui-sans-serif, system-ui, -apple-system, Segoe UI, Roboto, sans-serif";
const MONO = "ui-monospace, SFMono-Regular, Menlo, Consolas, monospace";

/** escape renders a value as SVG text content. */
function escape(text: string): string {
  return text
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

/** clip cuts a line to what fits the tile at the given character width. */
function clip(text: string, chars: number): string {
  return text.length > chars ? `${text.slice(0, chars - 1)}...` : text;
}

/** A drawn line of the document: its text and how the family renders it. */
interface Line {
  text: string;
  weight: number;
  size: number;
  mono: boolean;
  muted: boolean;
}

/** markdownLines draws headings larger and bolder, as the prose CSS does. */
function markdownLines(content: string): Line[] {
  return content.split("\n").map((raw) => {
    const heading = /^(#{1,3})\s+(.*)$/.exec(raw.trim());
    if (heading) {
      const level = heading[1]!.length;
      // 1.5em, 1.25em and 1.1em, as markdownProseCss sizes them.
      const em = level === 1 ? 1.5 : level === 2 ? 1.25 : 1.1;
      return {
        text: heading[2]!,
        weight: level === 1 ? 700 : 600,
        size: Math.round(BASE * em),
        mono: false,
        muted: false,
      };
    }
    const listItem = /^\s*[-*]\s+(.*)$/.exec(raw);
    return {
      text: listItem ? `• ${listItem[1]}` : raw,
      weight: 400,
      size: BASE,
      mono: false,
      muted: false,
    };
  });
}

/** codeLines draws a monospace family: SQL, plain text, JSON. */
function codeLines(content: string, mono: boolean): Line[] {
  return content
    .split("\n")
    .map((raw) => ({
      text: raw,
      weight: 400,
      size: BASE - 1,
      mono,
      muted: raw.trimStart().startsWith("--"),
    }));
}

/**
 * csvTile draws the header row and the rows under it as a table, which is what
 * the capturer's CSV body renders.
 */
function csvTile(content: string, scheme: (typeof SCHEMES)[keyof typeof SCHEMES]): string {
  const rows = content
    .split("\n")
    .filter((r) => r.trim() !== "")
    .slice(0, MAX_LINES);
  const header = rows[0]?.split(",") ?? [];
  const cols = Math.min(header.length, 6);
  const colWidth = (WIDTH - PAD * 2) / Math.max(cols, 1);
  const cells: string[] = [];
  const ROW = 19;
  rows.forEach((row, r) => {
    const y = PAD + 14 + r * ROW;
    if (r === 0) {
      cells.push(
        `<rect x="${PAD}" y="${y - 13}" width="${WIDTH - PAD * 2}" height="${ROW}" fill="${scheme.head}"/>`,
      );
    }
    // A quoted cell may hold a comma; the tile is a picture of the head of the
    // file, so a naive split is enough and a torn cell still reads as one.
    row.split(",").slice(0, cols).forEach((cell, c) => {
      const x = PAD + c * colWidth + 4;
      const chars = Math.max(Math.floor(colWidth / 5.6), 4);
      cells.push(
        `<text x="${x}" y="${y}" font-family="${MONO}" font-size="9"` +
          ` font-weight="${r === 0 ? 600 : 400}" fill="${r === 0 ? scheme.fg : scheme.muted}">` +
          `${escape(clip(cell.trim(), chars))}</text>`,
      );
    });
    cells.push(
      `<line x1="${PAD}" y1="${y + 6}" x2="${WIDTH - PAD}" y2="${y + 6}" stroke="${scheme.rule}" stroke-width="0.5"/>`,
    );
  });
  return cells.join("\n");
}

/** proseTile draws a run of lines at the size and weight its family gives them. */
function proseTile(lines: Line[], scheme: (typeof SCHEMES)[keyof typeof SCHEMES]): string {
  let y = PAD + BASE;
  const drawn: string[] = [];
  for (const line of lines.slice(0, MAX_LINES)) {
    if (line.text.trim() === "") {
      y += LINE * 0.6;
      continue;
    }
    const chars = Math.floor((WIDTH - PAD * 2) / (line.size * (line.mono ? 0.6 : 0.52)));
    drawn.push(
      `<text x="${PAD}" y="${y}" font-family="${line.mono ? MONO : SANS}" font-size="${line.size}"` +
        ` font-weight="${line.weight}" fill="${line.muted ? scheme.muted : scheme.fg}">` +
        `${escape(clip(line.text.trimEnd(), chars))}</text>`,
    );
    y += Math.round(line.size * 1.6);
    if (y > HEIGHT - PAD) break;
  }
  return drawn.join("\n");
}

/**
 * resourceTileSVG is the tile for a resource of the given type and content, in
 * the requested scheme. A family the capturer does not draw as a document --
 * an image, an SVG, an iframe-rendered page -- returns undefined: those are
 * served from their own bytes rather than redrawn.
 */
export function resourceTileSVG(
  contentType: string,
  content: string,
  dark: boolean,
): string | undefined {
  const family = captureFamily(contentType);
  const scheme = dark ? SCHEMES.dark : SCHEMES.light;
  let body: string;
  switch (family) {
    case "csv":
      body = csvTile(content, scheme);
      break;
    case "markdown":
      body = proseTile(markdownLines(content), scheme);
      break;
    case "json":
      body = proseTile(codeLines(content, true), scheme);
      break;
    case "text":
      body = proseTile(codeLines(content, true), scheme);
      break;
    default:
      return undefined;
  }
  return `<svg xmlns="http://www.w3.org/2000/svg" width="${WIDTH}" height="${HEIGHT}" viewBox="0 0 ${WIDTH} ${HEIGHT}">
<rect width="${WIDTH}" height="${HEIGHT}" fill="${scheme.bg}"/>
${body}
</svg>`;
}

/**
 * Where a fixture resource's tile comes from, or null when the mock cannot
 * serve one.
 *
 * This is the one answer both halves read: the fixtures stamp a recorded
 * capture onto a resource only when it is not null, and the thumbnail route
 * serves what it names. Declaring a capture the route cannot answer is what
 * put a grid of file-type icons in the documentation (#1619), so the two are
 * decided in one place rather than kept in step by hand.
 *
 * An HTML resource has no entry: the real capturer renders it in an iframe and
 * rasterizes that, which the mock cannot do, so such a fixture carries no
 * capture and its tile is the file-type icon a resource waiting for its first
 * capture shows.
 */
export type FixtureTile =
  | { kind: "image"; bytes: Uint8Array; contentType: string }
  | { kind: "svg"; svg: string }
  | { kind: "drawn" }
  | null;

export function fixtureTile(id: string, contentType: string, content: string): FixtureTile {
  switch (captureFamily(contentType)) {
    case "csv":
    case "markdown":
    case "json":
    case "text":
      return { kind: "drawn" };
    case "image": {
      const bytes = resourceImageBytes(id);
      return bytes ? { kind: "image", bytes, contentType } : null;
    }
    case "svg":
      // Its own markup is its tile. A fixture whose content endpoint answers
      // with a placeholder rather than a document has nothing to draw.
      return content.trimStart().startsWith("<svg") ? { kind: "svg", svg: content } : null;
    default:
      return null;
  }
}
