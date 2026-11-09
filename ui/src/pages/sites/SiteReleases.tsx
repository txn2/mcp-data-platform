import { useSiteReleases, type Site } from "@/api/portal/hooks/sites";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { formatBytes, when } from "./siteFormat";

// SiteReleasesTab is every release (#2559). A release's content is never shown
// inside the portal: it opens to its file list, and its pages open on the
// site's own origin.
export function SiteReleasesTab({ site, onNavigate }: { site: Site; onNavigate: (path: string) => void }) {
  const { data, isLoading, error } = useSiteReleases(site.name);
  return (
    <SectionCard title="Releases">
      {error ? (
        <Alert variant="destructive">
          <AlertDescription>{error.message}</AlertDescription>
        </Alert>
      ) : isLoading ? (
        <p className="text-sm text-muted-foreground">Loading…</p>
      ) : (data?.releases.length ?? 0) === 0 ? (
        <p className="text-sm text-muted-foreground">No releases yet. Publish the first one from Environments.</p>
      ) : (
        <div className="overflow-x-auto">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Release</TableHead>
                <TableHead>Note</TableHead>
                <TableHead>Source</TableHead>
                <TableHead>Files</TableHead>
                <TableHead>Size</TableHead>
                <TableHead>Published</TableHead>
                <TableHead>Serving in</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {data!.releases.map((r) => (
                <TableRow key={r.id}>
                  <TableCell>
                    <button type="button" className="font-medium text-primary hover:underline" onClick={() => onNavigate(`/sites/${encodeURIComponent(site.name)}/releases/${r.number}`)}>
                      {r.number}
                    </button>
                  </TableCell>
                  <TableCell>{r.note}</TableCell>
                  <TableCell className="break-all">{r.source}</TableCell>
                  <TableCell>{r.file_count}</TableCell>
                  <TableCell>{formatBytes(r.total_bytes)}</TableCell>
                  <TableCell>
                    <div>{when(r.created_at)}</div>
                    <div className="text-xs text-muted-foreground">{r.created_by}</div>
                  </TableCell>
                  <TableCell>
                    <div className="flex flex-wrap gap-1">
                      {r.served_in.length === 0 && <span className="text-muted-foreground">None</span>}
                      {r.served_in.map((e) => (
                        <Badge key={e} variant="info">
                          {e}
                        </Badge>
                      ))}
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
    </SectionCard>
  );
}
