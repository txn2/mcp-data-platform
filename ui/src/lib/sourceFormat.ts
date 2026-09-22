/**
 * The source editor's Format action (#1839).
 *
 * Which families can be formatted is answered here without loading anything:
 * the editor asks on every render to decide whether to show the button.
 * Prettier itself is imported inside formatSource, so its standalone core and
 * the plugins a parser needs are separate chunks fetched on the first click.
 * Nothing in the initial portal bundle or the SourceEditor chunk carries them.
 *
 * Formatting only ever produces a new buffer. Storing it is the editor's
 * ordinary Save, so content an agent wrote is never rewritten unasked.
 */

import type { Plugin } from "prettier";
import { CT } from "@/lib/contentType";
import { resolveRenderer } from "@/components/renderers/registry";
import { rendersSameText } from "@/lib/htmlRenderCheck";

/** The Prettier parser a family is formatted with. */
export type FormatParser = "html" | "json" | "babel" | "yaml" | "markdown";

/**
 * The parser for a content type, or undefined when the family has no
 * formatter. JSON Lines shares JSON's editor mode but is one document per line,
 * which a JSON formatter would reindent into something that is no longer JSON
 * Lines, so it is left out.
 */
export function formatParserFor(contentType: string, fileName?: string): FormatParser | undefined {
  const resolved = resolveRenderer({ contentType, fileName });
  if (resolved.contentType === CT.ndjson) return undefined;
  switch (resolved.language) {
    case "html":
      return "html";
    case "json":
      return "json";
    case "javascript":
      return "babel";
    case "yaml":
      return "yaml";
    case "markdown":
      return "markdown";
    default:
      return undefined;
  }
}

/**
 * Formats text with the parser's Prettier plugins. Rejects with Prettier's
 * syntax error when the input does not parse; it never returns a partial
 * result.
 *
 * HTML keeps Prettier's default whitespace sensitivity ("css"): whitespace is
 * added only where the element's default CSS display makes it invisible, and
 * `<pre>` and `<textarea>` bodies are left as written, so the document renders
 * the same after formatting. Its `<style>` and `<script>` bodies are formatted
 * as CSS and JavaScript, which is why HTML loads those plugins too.
 */
export async function formatSource(text: string, parser: FormatParser): Promise<string> {
  const [{ format }, plugins] = await Promise.all([import("prettier/standalone"), pluginsFor(parser)]);
  return format(text, { parser, plugins });
}

/** Why an HTML format was refused when its output would display differently. */
export const RENDER_CHANGED_MESSAGE =
  "Formatting would change how this document's text displays (an element keeps its whitespace), so the source was left as it was.";

/**
 * formatSource, as the source editor's Format action runs it. HTML output is
 * accepted only when it lays out to the same text as the input (see
 * htmlRenderCheck); otherwise it rejects with RENDER_CHANGED_MESSAGE and the
 * caller keeps the buffer it had.
 */
export async function formatForEditor(
  text: string,
  parser: FormatParser,
  sameRendering: (before: string, after: string) => Promise<boolean> = rendersSameText,
): Promise<string> {
  const formatted = await formatSource(text, parser);
  if (parser === "html" && formatted !== text && !(await sameRendering(text, formatted))) {
    throw new Error(RENDER_CHANGED_MESSAGE);
  }
  return formatted;
}

async function pluginsFor(parser: FormatParser): Promise<Plugin[]> {
  const loaded = await Promise.all(pluginLoaders(parser).map((load) => load()));
  return loaded.map((m) => m.default ?? m);
}

type PluginModule = Plugin & { default?: Plugin };

function pluginLoaders(parser: FormatParser): Array<() => Promise<PluginModule>> {
  const babel = () => import("prettier/plugins/babel") as Promise<PluginModule>;
  const estree = () => import("prettier/plugins/estree") as Promise<PluginModule>;
  switch (parser) {
    case "html":
      return [
        () => import("prettier/plugins/html") as Promise<PluginModule>,
        () => import("prettier/plugins/postcss") as Promise<PluginModule>,
        babel,
        estree,
      ];
    case "json":
    case "babel":
      return [babel, estree];
    case "yaml":
      return [() => import("prettier/plugins/yaml") as Promise<PluginModule>];
    case "markdown":
      return [() => import("prettier/plugins/markdown") as Promise<PluginModule>];
  }
}

/**
 * A formatter error, short enough for a toolbar: its first sentence and the
 * position it names ('Unexpected closing tag "div". (1:12)'). Prettier's
 * message goes on to explain the HTML rule, link the specification, and
 * append a code frame.
 */
export function formatErrorMessage(err: unknown): string {
  const message = err instanceof Error ? err.message : String(err);
  const firstLine = message.split("\n", 1)[0]?.trim() ?? "";
  if (!firstLine) return "The source could not be formatted.";
  const position = /\((\d+:\d+)\)$/.exec(firstLine)?.[1];
  const sentence = firstLine.replace(/\s*\(\d+:\d+\)$/, "").split(/(?<=\.)\s/, 1)[0]!;
  return position ? `${sentence} (${position})` : sentence;
}

/** The length, in characters, of the longest line in a text. */
export function longestLineLength(text: string): number {
  let longest = 0;
  let start = 0;
  for (;;) {
    const end = text.indexOf("\n", start);
    const len = (end < 0 ? text.length : end) - start;
    if (len > longest) longest = len;
    if (end < 0) return longest;
    start = end + 1;
  }
}

/**
 * True when the longest line would run past the editor's text area. An editor
 * that is not laid out yet (a hidden tab, width 0) is never judged to overflow.
 */
export function overflowsEditor(text: string, charWidth: number, textWidth: number): boolean {
  if (textWidth <= 0 || charWidth <= 0) return false;
  return longestLineLength(text) * charWidth > textWidth;
}
