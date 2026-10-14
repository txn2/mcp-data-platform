import { useState } from "react";
import { useSchemaDocument } from "@/api/portal/catalog";
import { Button } from "@/components/ui/button";
import { formatBytes } from "@/lib/format";

/** SchemaDocumentPanel shows the schema document (an OpenAPI spec, a GraphQL
 * SDL) a dataset was read from, as the catalog scan last stored it. The text is
 * folded until asked for, since a spec runs to thousands of lines. */
export function SchemaDocumentPanel({ reference }: { reference: string }) {
  const { data, isLoading, isError } = useSchemaDocument(reference);
  const [shown, setShown] = useState(false);
  if (isLoading) return <p className="text-sm text-muted-foreground">Loading…</p>;
  if (isError || !data) {
    return <p className="text-sm text-destructive">The schema document could not be read.</p>;
  }
  return (
    <div className="space-y-2">
      <p className="flex flex-wrap items-center gap-x-3 text-sm">
        <span className="font-mono">{data.filename}</span>
        <span className="text-xs text-muted-foreground">
          {data.mime_type} · {formatBytes(data.size_bytes)} · version {data.version} · read{" "}
          {new Date(data.updated_at).toLocaleString()}
        </span>
        <Button variant="ghost" size="xs" onClick={() => setShown(!shown)}>
          {shown ? "Hide" : "Show"}
        </Button>
      </p>
      {shown && (
        <pre className="max-h-96 overflow-auto rounded-md border bg-muted/40 p-3 font-mono text-xs">
          {data.content}
        </pre>
      )}
    </div>
  );
}
