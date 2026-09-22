import { useEffect, useMemo, useState } from "react";
import { ChevronLeft, ChevronRight, Download } from "lucide-react";
import { asyncBufferFromUrl, parquetMetadataAsync, parquetReadObjects, type AsyncBuffer, type FileMetaData } from "hyparquet";
import { decompressors } from "./parquet/decompressors";
import { authedFetch } from "@/api/authed";
import { Button } from "@/components/ui/button";
import { formatBytes } from "@/lib/format";
import { DataTable, MAX_DISPLAY_ROWS } from "./DataTable";
import { codecsOf, parquetColumns } from "./parquet/schema";

interface ParquetRendererProps {
  contentUrl: string;
  fileName?: string;
  sizeBytes?: number;
}

/** The open file: the byte source every read goes through, and its footer. */
interface OpenFile {
  file: AsyncBuffer;
  metadata: FileMetaData;
}

/**
 * Parquet viewer (#1833): the file's schema, the facts its footer states, and
 * its rows one row group at a time.
 *
 * Nothing is fetched whole. The footer is read by range from the end of the
 * file, and each page of rows reads only the column chunks of its row group,
 * so a large file costs what is on screen. Every read goes to the content
 * endpoint the other viewers use, which answers a Range request with the bytes
 * asked for (blobserve).
 *
 * The schema panel names the Trino type each column registers as, by the
 * mapping manage_table registers a Parquet file with, so the viewer and a
 * registration agree about what the table would be.
 */
export function ParquetRenderer({ contentUrl, fileName, sizeBytes }: ParquetRendererProps) {
  const [open, setOpen] = useState<OpenFile | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    setOpen(null);
    setError(null);
    (async () => {
      // authedFetch carries the session's credential, which an API-key session
      // holds only in JavaScript, so a portal page reads by range as a cookie
      // session does; a public share page has none and needs none.
      const file = await asyncBufferFromUrl({
        url: contentUrl,
        byteLength: sizeBytes || undefined,
        fetch: (url, init) => authedFetch(String(url), init),
      });
      const metadata = await parquetMetadataAsync(file);
      if (!cancelled) setOpen({ file, metadata });
    })().catch((err: unknown) => {
      if (!cancelled) setError(err instanceof Error ? err.message : String(err));
    });
    return () => {
      cancelled = true;
    };
  }, [contentUrl, sizeBytes]);

  const download = (
    <Button asChild variant="outline" className="shrink-0">
      <a href={contentUrl} download={fileName}>
        <Download />
        Download
      </a>
    </Button>
  );

  if (error) {
    return (
      <div className="space-y-3 rounded-lg border bg-card p-6 text-sm" data-feedback-anchorable>
        <p>This Parquet file could not be read: {error}</p>
        {download}
      </div>
    );
  }
  if (!open) {
    return <div className="py-16 text-center text-sm text-muted-foreground">Reading the file&apos;s footer...</div>;
  }
  return (
    <div className="space-y-4" data-feedback-anchorable>
      <FileFacts metadata={open.metadata} sizeBytes={open.file.byteLength} />
      <SchemaPanel metadata={open.metadata} />
      <RowGroupPages open={open} download={download} />
    </div>
  );
}

function FileFacts({ metadata, sizeBytes }: { metadata: FileMetaData; sizeBytes: number }) {
  const facts: Array<[string, string]> = [
    ["Rows", metadata.num_rows.toLocaleString()],
    ["Row groups", String(metadata.row_groups.length)],
    ["Size", formatBytes(sizeBytes)],
    ["Compression", codecsOf(metadata).join(", ") || "none"],
    ["Written by", metadata.created_by ?? "not recorded"],
  ];
  return (
    <dl className="grid grid-cols-2 gap-x-6 gap-y-1 rounded-lg border bg-card p-4 text-sm sm:grid-cols-5" data-testid="parquet-facts">
      {facts.map(([label, value]) => (
        <div key={label} className="min-w-0">
          <dt className="text-xs text-muted-foreground">{label}</dt>
          <dd className="truncate" title={value}>
            {value}
          </dd>
        </div>
      ))}
    </dl>
  );
}

function SchemaPanel({ metadata }: { metadata: FileMetaData }) {
  const columns = useMemo(() => parquetColumns(metadata), [metadata]);
  return (
    <div className="overflow-x-auto rounded-lg border bg-card" data-testid="parquet-schema">
      <table className="w-full text-sm">
        <thead className="bg-muted/40 text-left text-xs text-muted-foreground">
          <tr>
            <th className="px-3 py-2 font-medium">Column</th>
            <th className="px-3 py-2 font-medium">Parquet type</th>
            <th className="px-3 py-2 font-medium">Nullable</th>
            <th className="px-3 py-2 font-medium">Registers as</th>
          </tr>
        </thead>
        <tbody className="divide-y">
          {columns.map((c) => (
            <tr key={c.name}>
              <td className="px-3 py-1.5 font-mono text-xs">{c.name}</td>
              <td className="px-3 py-1.5 font-mono text-xs">{c.parquetType}</td>
              <td className="px-3 py-1.5 text-xs">{c.nullable ? "yes" : "no"}</td>
              <td className="px-3 py-1.5 font-mono text-xs">
                {c.trinoType ? (
                  <>
                    {c.trinoType}
                    {/* A registration lowercases a column name, so a panel
                        headed "Registers as" that showed Store_ID would name a
                        column no query finds. */}
                    {c.name !== c.name.toLowerCase() && (
                      <span className="ml-2 font-sans text-muted-foreground">as {c.name.toLowerCase()}</span>
                    )}
                  </>
                ) : (
                  <span className="font-sans text-amber-700 dark:text-amber-400">not registrable: {c.refused}</span>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

/** One row group's rows, the first MAX_DISPLAY_ROWS of it, with paging between groups. */
function RowGroupPages({ open, download }: { open: OpenFile; download: React.ReactNode }) {
  const groups = open.metadata.row_groups;
  const [group, setGroup] = useState(0);
  const [rows, setRows] = useState<Record<string, unknown>[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const names = useMemo(() => parquetColumns(open.metadata).map((c) => c.name), [open.metadata]);

  const start = useMemo(
    () => groups.slice(0, group).reduce((sum, g) => sum + Number(g.num_rows), 0),
    [groups, group],
  );
  const groupRows = Number(groups[group]?.num_rows ?? 0);

  useEffect(() => {
    let cancelled = false;
    setRows(null);
    setError(null);
    parquetReadObjects({
      file: open.file,
      metadata: open.metadata,
      rowStart: start,
      rowEnd: start + Math.min(groupRows, MAX_DISPLAY_ROWS),
      compressors: decompressors,
    })
      .then((read) => {
        if (!cancelled) setRows(read);
      })
      .catch((err: unknown) => {
        if (!cancelled) setError(err instanceof Error ? err.message : String(err));
      });
    return () => {
      cancelled = true;
    };
  }, [open, start, groupRows]);

  return (
    <div className="space-y-2" data-testid="parquet-rows">
      {groups.length > 1 && (
        <div className="flex items-center gap-2 text-sm">
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            aria-label="Previous row group"
            disabled={group === 0}
            onClick={() => setGroup((g) => g - 1)}
          >
            <ChevronLeft />
          </Button>
          <span>
            Row group {group + 1} of {groups.length}
          </span>
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            aria-label="Next row group"
            disabled={group >= groups.length - 1}
            onClick={() => setGroup((g) => g + 1)}
          >
            <ChevronRight />
          </Button>
        </div>
      )}
      {error && <p className="text-sm text-amber-700 dark:text-amber-400">These rows could not be read: {error}</p>}
      {!error && !rows && <p className="py-8 text-center text-sm text-muted-foreground">Reading rows...</p>}
      {rows && (
        <DataTable
          columns={names}
          rows={rows}
          actions={download}
          footer={
            groupRows > MAX_DISPLAY_ROWS ? (
              <p className="text-xs text-muted-foreground">
                The first {MAX_DISPLAY_ROWS} of this row group&apos;s {groupRows.toLocaleString()} rows are shown.
              </p>
            ) : null
          }
        />
      )}
    </div>
  );
}
