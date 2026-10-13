import { Download } from "lucide-react";
import { catalogDocumentHref, useCatalogDocuments } from "@/api/admin/hooks";
import { EmptyState } from "@/components/patterns/EmptyState";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { formatBytes } from "@/lib/format";

// DocumentsTab lists each connection's schema document as the catalog scan
// last read it (#1918): an API connection's OpenAPI spec, a GraphQL
// connection's SDL. An agent reads one through fetch, and only when its
// persona may use the connection; an administrator downloads it here.
export function DocumentsTab() {
  const { data, isLoading, isError } = useCatalogDocuments();
  const docs = data?.documents;
  if (isError) {
    return (
      <Alert variant="destructive" className="py-2">
        <AlertDescription>The schema documents could not be read.</AlertDescription>
      </Alert>
    );
  }
  if (!isLoading && docs?.length === 0) {
    return <EmptyState>No API or GraphQL connection has been scanned yet.</EmptyState>;
  }
  return (
    <div className="rounded-md border">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Connection</TableHead>
            <TableHead>Kind</TableHead>
            <TableHead>File</TableHead>
            <TableHead className="text-right">Size</TableHead>
            <TableHead className="text-right">Version</TableHead>
            <TableHead>Updated</TableHead>
            <TableHead className="w-0" />
          </TableRow>
        </TableHeader>
        <TableBody>
          {(docs ?? []).map((d) => (
            <TableRow key={d.reference}>
              <TableCell className="font-medium">{d.connection}</TableCell>
              <TableCell>{d.connection_kind}</TableCell>
              <TableCell className="font-mono text-xs">{d.filename}</TableCell>
              <TableCell className="text-right tabular-nums">{formatBytes(d.size_bytes)}</TableCell>
              <TableCell className="text-right tabular-nums">{d.version}</TableCell>
              <TableCell>{new Date(d.updated_at).toLocaleString()}</TableCell>
              <TableCell>
                <Button asChild size="sm" variant="ghost">
                  <a href={catalogDocumentHref(d)} download={d.filename}>
                    <Download />
                    Download
                  </a>
                </Button>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}
