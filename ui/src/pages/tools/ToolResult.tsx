import type { ToolCallResponse } from "@/api/admin/types";
import { StatusBadge } from "@/components/cards/StatusBadge";
import { formatDuration } from "@/lib/formatDuration";

interface ToolResultProps {
  result: ToolCallResponse;
  toolKind: string;
  showRaw: boolean;
  onToggleRaw: () => void;
}

export function ToolResult({
  result,
  toolKind,
  showRaw,
  onToggleRaw,
}: ToolResultProps) {
  const hasContent = result.content.length > 0;
  const primary = result.content[0]?.text ?? "";
  const enrichmentBlocks = result.content.slice(1);
  const rawText = result.content.map((c) => c.text).join("\n\n");

  return (
    <div
      className={`rounded-lg border ${result.is_error ? "border-red-200 bg-red-50" : "bg-card"} p-4`}
    >
      <div className="mb-3 flex items-center gap-3">
        <StatusBadge variant={result.is_error ? "error" : "success"}>
          {result.is_error ? "Error" : "Success"}
        </StatusBadge>
        <StatusBadge variant="neutral">
          {formatDuration(result.duration_ms)}
        </StatusBadge>
        {enrichmentBlocks.length > 0 && (
          <StatusBadge variant="success">Enriched</StatusBadge>
        )}
        <button
          onClick={onToggleRaw}
          className="ml-auto rounded px-2 py-0.5 text-xs text-muted-foreground hover:bg-muted hover:text-foreground"
        >
          {showRaw ? "Formatted" : "Raw"}
        </button>
      </div>

      {!hasContent ? (
        <p className="text-sm text-muted-foreground">
          {result.is_error
            ? "Tool call failed. Check server logs for details."
            : "Tool returned no content."}
        </p>
      ) : showRaw ? (
        <pre className="max-h-[500px] overflow-auto rounded bg-muted p-3 font-mono text-xs">
          {rawText}
        </pre>
      ) : (
        <div className="space-y-4">
          <div>
            <p className="mb-1.5 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
              Tool Result
            </p>
            <FormattedBlock text={primary} kind={toolKind} />
          </div>
          {enrichmentBlocks.map((block, idx) => {
            const label =
              detectEnrichmentLabel(block.text) ?? `Enrichment ${idx + 1}`;
            return (
              <div key={idx} className="border-t pt-3">
                <p className="mb-1.5 text-[11px] font-medium uppercase tracking-wide text-primary">
                  {label}
                </p>
                <EnrichmentBlock text={block.text} />
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}

function detectEnrichmentLabel(text: string): string | null {
  try {
    const obj = JSON.parse(text);
    if (obj.semantic_context) return "Semantic Context";
    if (obj.query_context) return "Query Context";
    if (obj.column_context) return "Column Context";
    if (obj.storage_context) return "Storage Context";
    if (obj.metadata_reference) return "Metadata Reference";
  } catch {
    // not JSON
  }
  return null;
}

function FormattedBlock({ text, kind }: { text: string; kind: string }) {
  if (!text) {
    return <p className="text-sm italic text-muted-foreground">(empty response)</p>;
  }
  if (kind === "trino" && text.includes("|")) {
    return <MarkdownTableView text={text} />;
  }
  try {
    const parsed = JSON.parse(text);
    return (
      <pre className="max-h-[400px] overflow-auto rounded bg-muted p-3 font-mono text-xs">
        {JSON.stringify(parsed, null, 2)}
      </pre>
    );
  } catch {
    // plain text
  }
  return (
    <pre className="max-h-[400px] overflow-auto whitespace-pre-wrap rounded bg-muted p-3 font-mono text-xs">
      {text}
    </pre>
  );
}

function EnrichmentBlock({ text }: { text: string }) {
  try {
    const obj = JSON.parse(text);
    return (
      <div className="space-y-2">
        {Object.entries(obj).map(([key, value]) => (
          <div key={key}>
            <p className="mb-1 text-xs font-medium text-muted-foreground">
              {key.replace(/_/g, " ").replace(/\b\w/g, (c) => c.toUpperCase())}
            </p>
            <pre className="max-h-[250px] overflow-auto rounded border border-primary/10 bg-primary/5 p-3 font-mono text-xs">
              {JSON.stringify(value, null, 2)}
            </pre>
          </div>
        ))}
      </div>
    );
  } catch {
    return (
      <pre className="max-h-[250px] overflow-auto whitespace-pre-wrap rounded bg-muted p-3 font-mono text-xs">
        {text}
      </pre>
    );
  }
}

function MarkdownTableView({ text }: { text: string }) {
  const lines = text.split("\n");
  const tableLines = lines.filter(
    (l) => l.trim().startsWith("|") && l.trim().endsWith("|"),
  );
  const otherLines = lines.filter(
    (l) => !(l.trim().startsWith("|") && l.trim().endsWith("|")),
  );

  if (tableLines.length < 2) {
    return (
      <pre className="max-h-[400px] overflow-auto whitespace-pre-wrap rounded bg-muted p-3 font-mono text-xs">
        {text}
      </pre>
    );
  }

  const parseRow = (line: string) =>
    line
      .split("|")
      .slice(1, -1)
      .map((c) => c.trim());

  const headers = parseRow(tableLines[0]!);
  const rows = tableLines.slice(2).map(parseRow);

  return (
    <div className="space-y-2">
      <div className="max-h-[400px] overflow-auto rounded border">
        <table className="w-full text-xs">
          <thead>
            <tr className="border-b bg-muted/50">
              {headers.map((h, i) => (
                <th key={i} className="px-3 py-2 text-left font-medium">
                  {h}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {rows.map((row, ri) => (
              <tr key={ri} className="border-b">
                {row.map((cell, ci) => (
                  <td key={ci} className="px-3 py-1.5 font-mono">
                    {cell}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {otherLines.filter((l) => l.trim()).length > 0 && (
        <p className="text-xs text-muted-foreground">
          {otherLines.filter((l) => l.trim()).join(" ")}
        </p>
      )}
    </div>
  );
}
