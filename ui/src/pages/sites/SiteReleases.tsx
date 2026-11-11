import { useSiteReleases, type Site, type SiteRelease } from "@/api/portal/hooks/sites";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { day, when } from "./siteFormat";

// SiteReleasesTab is every release (#2559): what each was based on (#2590) and
// what keeps it from the daily cleanup (#2602). A release's content is never
// shown inside the portal: it opens to its file list, and its pages open on the
// site's own origin. A removed release stays listed as history, with no link.
export function SiteReleasesTab({ site, onNavigate }: { site: Site; onNavigate: (path: string) => void }) {
  const { data, isLoading, error } = useSiteReleases(site.name);
  const open = (n: number) => onNavigate(`/sites/${encodeURIComponent(site.name)}/releases/${n}`);
  const releases = data?.releases ?? [];
  return (
    <SectionCard title="Releases">
      {error ? (
        <Alert variant="destructive">
          <AlertDescription>{error.message}</AlertDescription>
        </Alert>
      ) : isLoading ? (
        <p className="text-sm text-muted-foreground">Loading…</p>
      ) : releases.length === 0 ? (
        <p className="text-sm text-muted-foreground">No releases yet. Publish the first one from Environments.</p>
      ) : (
        <div className="space-y-3">
          <div className="overflow-x-auto">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Release</TableHead>
                  <TableHead>Note</TableHead>
                  <TableHead>Source</TableHead>
                  <TableHead>Based on</TableHead>
                  <TableHead>Files</TableHead>
                  <TableHead>Published</TableHead>
                  <TableHead>Serving in</TableHead>
                  <TableHead>Kept by</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {releases.map((r) => (
                  <ReleaseRow key={r.id} release={r} gone={removedNumbers(releases)} onOpen={open} />
                ))}
              </TableBody>
            </Table>
          </div>
          <ReleasesNote releases={releases} />
        </div>
      )}
    </SectionCard>
  );
}

function removedNumbers(releases: SiteRelease[]): Set<number> {
  return new Set(releases.filter((r) => r.pruned).map((r) => r.number));
}

function ReleaseRow({ release: r, gone, onOpen }: { release: SiteRelease; gone: Set<number>; onOpen: (n: number) => void }) {
  return (
    <TableRow data-testid={`release-${r.number}`} className={r.pruned ? "text-muted-foreground" : undefined}>
      <TableCell>
        {r.pruned ? (
          <span className="font-medium">{r.number}</span>
        ) : (
          <button type="button" className="font-medium text-primary hover:underline" onClick={() => onOpen(r.number)}>
            {r.number}
          </button>
        )}
      </TableCell>
      <TableCell>{r.note}</TableCell>
      <TableCell className="break-all">{r.source}</TableCell>
      <TableCell>
        <BasedOn base={r.base ?? 0} gone={gone} onOpen={onOpen} />
      </TableCell>
      <TableCell>{r.pruned ? "—" : r.file_count.toLocaleString()}</TableCell>
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
      <TableCell>
        <KeptBy release={r} />
      </TableCell>
    </TableRow>
  );
}

function BasedOn({ base, gone, onOpen }: { base: number; gone: Set<number>; onOpen: (n: number) => void }) {
  if (!base) return <span className="text-muted-foreground">None</span>;
  if (gone.has(base)) return <span>{base}</span>;
  return (
    <button type="button" className="text-primary hover:underline" onClick={() => onOpen(base)}>
      {base}
    </button>
  );
}

/** keptByLabel is how one reason a release is kept reads. */
function keptByLabel(reason: string): string {
  if (reason === "approval") return "Approval";
  if (reason.startsWith("application ")) return `Application ${reason.slice("application ".length)}`;
  return reason;
}

function KeptBy({ release: r }: { release: SiteRelease }) {
  if (r.pruned) {
    return (
      <Badge variant="muted" data-testid="release-removed">
        Removed{r.pruned_at ? ` ${day(r.pruned_at)}` : ""}
      </Badge>
    );
  }
  if (r.kept_by.length === 0) return <span className="text-xs text-muted-foreground">None · removed at the next cleanup</span>;
  return (
    <div className="flex flex-wrap gap-1">
      {r.kept_by.map((k) => (
        <Badge key={k} variant="muted">
          {keptByLabel(k)}
        </Badge>
      ))}
    </div>
  );
}

function ReleasesNote({ releases }: { releases: SiteRelease[] }) {
  const removed = releases.filter((r) => r.pruned);
  return (
    <div className="space-y-1 text-xs text-muted-foreground">
      <p>A release based on another holds that release’s files with one change made: an edit, an upload or a deletion. A release with no base was published whole.</p>
      <p>
        Kept by lists the environments whose limit keeps a release.
        {removed.length > 0 &&
          ` ${removed.length} ${removed.length === 1 ? "release that no environment kept was" : "releases that no environment kept were"} removed by the daily cleanup: they stay listed as history, and can’t be opened, promoted or rolled back to.`}
      </p>
    </div>
  );
}
