import { useMemo, useCallback } from "react";
import Papa from "papaparse";
import { Download } from "lucide-react";
import { Button } from "@/components/ui/button";
import { DataTable } from "./DataTable";

interface Props {
  content: string;
  fileName?: string;
  /**
   * Field delimiter. Defaults to a comma; the registry passes a tab for
   * text/tab-separated-values, which is otherwise the same format and the same
   * viewer.
   */
  delimiter?: "," | "\t";
}

export function CsvRenderer({ content, fileName, delimiter = "," }: Props) {
  const isTsv = delimiter === "\t";
  const downloadName = fileName || (isTsv ? "data.tsv" : "data.csv");

  const parsed = useMemo(
    () =>
      Papa.parse<Record<string, unknown>>(content, {
        header: true,
        skipEmptyLines: true,
        dynamicTyping: true,
        delimiter,
      }),
    [content, delimiter],
  );

  const columns = useMemo(() => parsed.meta.fields ?? [], [parsed]);

  const handleDownload = useCallback(() => {
    const blob = new Blob([content], {
      type: isTsv ? "text/tab-separated-values" : "text/csv",
    });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = downloadName;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(url);
  }, [content, downloadName, isTsv]);

  if (columns.length === 0) {
    return (
      <pre className="rounded-lg border bg-card p-6 text-sm overflow-auto whitespace-pre-wrap">
        {content}
      </pre>
    );
  }

  return (
    <DataTable
      columns={columns}
      rows={parsed.data}
      actions={
        <Button
          type="button"
          variant="outline"
          onClick={handleDownload}
          className="shrink-0"
          title={isTsv ? "Download TSV" : "Download CSV"}
        >
          <Download />
          Download
        </Button>
      }
      footer={<ShapeNotice errors={parsed.errors} />}
    />
  );
}

/**
 * What the parser found wrong with the file's shape, which the viewer used to
 * throw away.
 *
 * PapaParse reports a record that does not have the header's fields as a
 * `FieldMismatch`, and this component read `parsed.data` and ignored
 * `parsed.errors`. A short record was drawn with its missing columns as empty
 * cells, so a file whose exporter omits trailing fields looked complete here
 * while a table registered over it answered that its records do not all have
 * the header's fields. Nobody looking at the viewer could tell (#1779).
 *
 * It is a note rather than a warning: a short record is ordinary and registers
 * (#1779). What it must not do is go unsaid.
 */
function ShapeNotice({ errors }: { errors: Papa.ParseError[] }) {
  const short = errors.filter((e) => e.code === "TooFewFields").length;
  const long = errors.filter((e) => e.code === "TooManyFields").length;
  if (short === 0 && long === 0) {
    return null;
  }
  const parts: string[] = [];
  if (short > 0) {
    parts.push(
      `${short} ${short === 1 ? "row ends" : "rows end"} before the last column; the columns after it are shown empty`,
    );
  }
  if (long > 0) {
    parts.push(
      `${long} ${long === 1 ? "row has" : "rows have"} more fields than the header names`,
    );
  }
  return (
    <p className="text-xs text-muted-foreground" data-testid="csv-shape-notice">
      {parts.join(". ")}.
    </p>
  );
}
