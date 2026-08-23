/**
 * The two JSON families as a thumbnail.
 *
 * A JSON document read as markdown prose came out as one unbroken line of text
 * (#1432). Each family is drawn in the shape its viewer shows it in instead: a
 * JSON document as the indented, syntax-colored source the Formatted view
 * holds, and a newline-delimited document as the numbered list of one-line
 * records the NDJSON viewer lists (renderers/NdjsonRenderer.tsx).
 */

import { jsonProseCss, ndjsonProseCss, type ProseTokens } from "./schemes";

/**
 * How much of a document is drawn.
 *
 * A thumbnail is 400x300 with the overflow clipped, so only the head of a
 * document is ever visible; these bound the tokenizing work rather than the
 * picture. A 1 MB array of records would otherwise be formatted and tokenized
 * in full on the main thread to fill a card 300 pixels tall.
 */
const MAX_JSON_LINES = 44;
const MAX_NDJSON_ROWS = 18;
/** Characters kept from one record before the rest is dropped. */
const MAX_RECORD_CHARS = 240;

/** The syntax classes a token is drawn in. `punct` is everything else. */
type JsonTone = "key" | "string" | "number" | "literal" | "punct";

interface JsonToken {
  text: string;
  tone: JsonTone;
}

/** A formatted JSON document as colored runs, one array per line. */
export type JsonThumbnailLines = JsonToken[][];

/**
 * A key is a quoted string a colon follows, which is a lookahead rather than
 * part of the match so the colon stays punctuation. Strings are matched before
 * the literal and number alternatives, so `true` or `42` inside a string value
 * is drawn as the string it belongs to.
 */
const JSON_TOKEN =
  /("(?:\\.|[^"\\])*")(?=\s*:)|("(?:\\.|[^"\\])*")|(true|false|null)|(-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?)/g;

function toneOf(m: RegExpMatchArray): JsonTone {
  if (m[1] !== undefined) return "key";
  if (m[2] !== undefined) return "string";
  if (m[3] !== undefined) return "literal";
  return "number";
}

/**
 * Splits one line into colored runs. Text between matches -- indentation,
 * braces, commas, colons -- becomes a `punct` run, so the runs concatenate back
 * to the line exactly and content that is not JSON at all still renders as the
 * text it is.
 */
export function tokenizeJsonLine(line: string): JsonToken[] {
  const out: JsonToken[] = [];
  let last = 0;
  for (const m of line.matchAll(JSON_TOKEN)) {
    const text = m[0] ?? "";
    const at = m.index ?? last;
    if (at > last) out.push({ text: line.slice(last, at), tone: "punct" });
    out.push({ text, tone: toneOf(m) });
    last = at + text.length;
  }
  if (last < line.length) out.push({ text: line.slice(last), tone: "punct" });
  return out;
}

/**
 * The head of a JSON document, re-indented and tokenized.
 *
 * Content that does not parse is tokenized as it stands rather than skipped:
 * that is what the viewer's Raw view shows for it, and a thumbnail of the
 * document is more use than the placeholder icon it would otherwise keep.
 */
export function buildJsonLines(content: string): JsonThumbnailLines {
  let text = content;
  try {
    text = JSON.stringify(JSON.parse(content), null, 2);
  } catch {
    // Not valid JSON; draw the source.
  }
  return text.split("\n").slice(0, MAX_JSON_LINES).map(tokenizeJsonLine);
}

export interface NdjsonThumbnailRecord {
  line: number;
  tokens: JsonToken[];
}

/**
 * The head of a newline-delimited document as numbered records. Blank lines are
 * skipped and the numbering follows the file, so the gutter names the line a
 * record is actually on, as the viewer's does.
 */
export function buildNdjsonRecords(content: string): NdjsonThumbnailRecord[] {
  const records: NdjsonThumbnailRecord[] = [];
  const lines = content.split("\n");
  for (let i = 0; i < lines.length && records.length < MAX_NDJSON_ROWS; i++) {
    const raw = (lines[i] ?? "").trim();
    if (raw === "") continue;
    records.push({ line: i + 1, tokens: tokenizeJsonLine(raw.slice(0, MAX_RECORD_CHARS)) });
  }
  return records;
}

function Runs({ tokens }: { tokens: JsonToken[] }) {
  return (
    <>
      {tokens.map((t, i) => (
        <span key={i} className={`jt-${t.tone}`}>
          {t.text}
        </span>
      ))}
    </>
  );
}

/** Formatted, colored JSON source. */
export function JsonThumbnailBody({
  lines,
  tokens,
  scope,
}: {
  lines: JsonThumbnailLines;
  tokens: ProseTokens;
  scope: string;
}) {
  return (
    <div>
      <style>{jsonProseCss(tokens, scope)}</style>
      <pre className={scope}>
        {lines.map((line, i) => (
          <div key={i}>{line.length === 0 ? "\u00a0" : <Runs tokens={line} />}</div>
        ))}
      </pre>
    </div>
  );
}

/** One clipped record per row, behind a line-number gutter. */
export function NdjsonThumbnailBody({
  records,
  tokens,
  scope,
}: {
  records: NdjsonThumbnailRecord[];
  tokens: ProseTokens;
  scope: string;
}) {
  return (
    <div className={scope}>
      <style>{ndjsonProseCss(tokens, scope)}</style>
      <table>
        <tbody>
          {records.map((r) => (
            <tr key={r.line}>
              <td className="jt-line">{r.line}</td>
              <td>
                <div className="jt-record">
                  <Runs tokens={r.tokens} />
                </div>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
