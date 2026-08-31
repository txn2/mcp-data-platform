import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import {
  csvProseCss,
  markdownProseCss,
  textProseCss,
  type ProseTokens,
} from "@/components/thumbnail/schemes";
import {
  JsonThumbnailBody,
  NdjsonThumbnailBody,
  type JsonThumbnailLines,
  type NdjsonThumbnailRecord,
} from "@/components/thumbnail/JsonThumbnailBody";

/**
 * The families the capturer draws into the page and rasterizes, rather than
 * into an iframe.
 *
 * They live here, beside the stylesheets they are drawn with, for the reason
 * the stylesheets do: every family the capturer grows adds a renderer and a
 * stylesheet, and keeping both in ThumbnailGenerator put it over its size
 * budget. What decides WHICH of these a content type is drawn as stays in the
 * capturer, derived from the one table that says what gets a thumbnail at all
 * (lib/thumbnailSupport).
 */
export type DomKind = "csv" | "svg" | "json" | "ndjson" | "markdown" | "text";

/** The parsed head of a CSV document: its header row and the rows drawn under it. */
export interface CsvTable {
  cols: string[];
  rows: Record<string, unknown>[];
}

/** One document's content as the family it belongs to, for one color scheme. */
export function DomBody({
  kind,
  tokens,
  scope,
  content,
  csvTable,
  sanitizedSvg,
  jsonLines,
  ndjsonRecords,
}: {
  kind: DomKind;
  tokens: ProseTokens;
  scope: string;
  content: string;
  csvTable: CsvTable | null;
  sanitizedSvg: string;
  jsonLines: JsonThumbnailLines | null;
  ndjsonRecords: NdjsonThumbnailRecord[] | null;
}) {
  if (kind === "csv" && csvTable) return <CsvBody table={csvTable} tokens={tokens} scope={scope} />;
  if (kind === "svg") return <div dangerouslySetInnerHTML={{ __html: sanitizedSvg }} />;
  if (kind === "text") return <TextBody content={content} tokens={tokens} scope={scope} />;
  if (kind === "json" && jsonLines) return <JsonThumbnailBody lines={jsonLines} tokens={tokens} scope={scope} />;
  if (kind === "ndjson" && ndjsonRecords) {
    return <NdjsonThumbnailBody records={ndjsonRecords} tokens={tokens} scope={scope} />;
  }
  return (
    <div style={{ maxWidth: "none" }}>
      <style>{markdownProseCss(tokens, scope)}</style>
      <div className={scope}>
        <ReactMarkdown remarkPlugins={[remarkGfm]}>{content}</ReactMarkdown>
      </div>
    </div>
  );
}

/**
 * How much of a plain-text file is drawn.
 *
 * The tile is 400x300 and holds on the order of twenty lines, so everything
 * past this is invisible; handing html2canvas a megabyte of text to lay out and
 * then clip is the cost the size cap exists to avoid, paid on the reader's main
 * thread for nothing.
 */
const MAX_TEXT_THUMBNAIL_CHARS = 4000;

/**
 * A plain-text file as its own tile.
 *
 * Plain text is one of the commonest things anyone uploads and it had no
 * thumbnail of either kind (#1568) -- and a .md stored as text/plain, which is
 * what every generic declaration used to produce, landed here too. It is drawn
 * as text rather than through the markdown renderer on purpose: the type says
 * the markup is not meant to be interpreted.
 */
function TextBody({
  content,
  tokens,
  scope,
}: {
  content: string;
  tokens: ProseTokens;
  scope: string;
}) {
  return (
    <div>
      <style>{textProseCss(tokens, scope)}</style>
      <pre className={scope}>{content.slice(0, MAX_TEXT_THUMBNAIL_CHARS)}</pre>
    </div>
  );
}

function CsvBody({ table, tokens, scope }: { table: CsvTable; tokens: ProseTokens; scope: string }) {
  return (
    <div>
      <style>{csvProseCss(tokens, scope)}</style>
      <div className={scope}>
        <table>
          <thead>
            <tr>
              {table.cols.map((col) => (
                <th key={col}>{col}</th>
              ))}
            </tr>
          </thead>
          <tbody>
            {table.rows.map((row, ri) => (
              <tr key={ri}>
                {table.cols.map((col) => (
                  <td key={col}>{String(row[col] ?? "")}</td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
