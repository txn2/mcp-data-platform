import { useMemo, useState } from "react";
import { Globe } from "lucide-react";
import { useSite, useSiteRelease, type Site, type SiteFile, type SiteRelease, type SiteReleaseDetail } from "@/api/portal/hooks/sites";
import { PageHeader } from "@/components/patterns/PageHeader";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { ChangesFrom } from "./SiteReleaseChanges";
import { day, envOpenUrl, formatBytes, when } from "./siteFormat";

const PAGE = 50;

function compressed(f: SiteFile): string {
  const parts = [f.gzip_size ? `gzip ${formatBytes(f.gzip_size)}` : "", f.brotli_size ? `brotli ${formatBytes(f.brotli_size)}` : ""].filter(Boolean);
  return parts.join(", ") || "Not compressed";
}

// SiteReleasePage is one release's file list and redirect map (#2559). It lists
// what the release holds and shows none of it: a release is never rendered
// inside the portal's origin.
export function SiteReleasePage({ site, number, onNavigate }: { site: string; number: number; onNavigate: (path: string) => void }) {
  const { data, isLoading, error } = useSiteRelease(site, number);
  const owner = useSite(site).data;
  const [filter, setFilter] = useState("");
  const [page, setPage] = useState(0);
  const back = () => onNavigate(`/sites/${encodeURIComponent(site)}/releases`);
  const files = useMemo(() => (data?.files ?? []).filter((f) => f.path.includes(filter)), [data, filter]);
  if (isLoading) return <p className="text-sm text-muted-foreground">Loading…</p>;
  if (error || !data) return <ReleaseFailure site={site} number={number} message={error?.message} onBack={back} />;
  const openAt = openAddress(owner, data.served_in);
  if (data.pruned) return <RemovedRelease site={site} release={data} onBack={back} />;
  const shown = files.slice(page * PAGE, (page + 1) * PAGE);
  const has404 = data.files.some((f) => f.path === "404.html");
  return (
    <div className="space-y-4">
      <PageHeader
        backLabel={site}
        onBack={back}
        icon={Globe}
        title={`Release ${number}`}
        subtitle={data.note}
        actions={
          <div className="flex flex-wrap items-center gap-2">
            {data.served_in.map((e) => (
              <Badge key={e} variant="info">
                {e}
              </Badge>
            ))}
            {openAt && (
              <Button size="sm" variant="outline" asChild>
                <a href={openAt} target="_blank" rel="noreferrer">
                  Open site
                </a>
              </Button>
            )}
          </div>
        }
      />
      <ReleaseFacts site={site} release={data} has404={has404} onNavigate={onNavigate} />
      {data.base ? <ChangesFrom site={site} from={data.base} to={number} manage={!!owner?.permissions.manage} /> : null}
      <SectionCard
        title="Files"
        action={
          <Input
            className="max-w-xs"
            placeholder="Filter by path"
            aria-label="Filter files by path"
            value={filter}
            onChange={(e) => {
              setFilter(e.target.value);
              setPage(0);
            }}
          />
        }
      >
        <div className="overflow-x-auto">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Path</TableHead>
                <TableHead>Type</TableHead>
                <TableHead>Size</TableHead>
                <TableHead>Compressed</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {shown.map((f) => (
                <TableRow key={f.path}>
                  <TableCell className="break-all font-mono text-sm">{f.path}</TableCell>
                  <TableCell>{f.content_type}</TableCell>
                  <TableCell>{formatBytes(f.size)}</TableCell>
                  <TableCell>{compressed(f)}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
        <div className="mt-3 flex items-center justify-between">
          <span className="text-xs text-muted-foreground">
            Showing {shown.length} of {files.length}
          </span>
          <div className="flex gap-2">
            <Button size="sm" variant="outline" disabled={page === 0} onClick={() => setPage(page - 1)}>
              Previous
            </Button>
            <Button size="sm" variant="outline" disabled={(page + 1) * PAGE >= files.length} onClick={() => setPage(page + 1)}>
              Next
            </Button>
          </div>
        </div>
      </SectionCard>
      <SectionCard title="Redirects" action={<span className="text-xs text-muted-foreground">{data.redirects.length} from _redirects</span>}>
        {data.redirects.length === 0 ? (
          <p className="text-sm text-muted-foreground">This release has no redirects.</p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>From</TableHead>
                <TableHead>To</TableHead>
                <TableHead>Status</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {data.redirects.map((r) => (
                <TableRow key={r.from}>
                  <TableCell className="break-all font-mono text-sm">{r.from}</TableCell>
                  <TableCell className="break-all font-mono text-sm">{r.to}</TableCell>
                  <TableCell>{r.status}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </SectionCard>
    </div>
  );
}

// RemovedRelease is a release the daily cleanup removed (#2602): its record
// stays as history, and its files are gone.
function RemovedRelease({ site, release: r, onBack }: { site: string; release: SiteRelease; onBack: () => void }) {
  return (
    <div className="space-y-4">
      <PageHeader backLabel={site} onBack={onBack} icon={Globe} title={`Release ${r.number}`} subtitle={r.note} actions={<Badge variant="muted">Removed</Badge>} />
      <Alert data-testid="release-removed">
        <AlertDescription>
          Release {r.number} was removed{r.pruned_at ? ` on ${day(r.pruned_at)}` : ""} by the daily cleanup, because no environment keeps it any longer. Its files are gone, so it can’t be opened, promoted or rolled back
          to. Published {when(r.created_at)} by {r.created_by}.
        </AlertDescription>
      </Alert>
    </div>
  );
}

/** Where a release can be opened: the first environment serving it that has an address. */
function openAddress(owner: Site | undefined, servedIn: string[]): string {
  if (!owner) return "";
  for (const env of servedIn) {
    const at = envOpenUrl(owner, env);
    if (at) return at;
  }
  return "";
}

function ReleaseFacts({ site, release: data, has404, onNavigate }: { site: string; release: SiteReleaseDetail; has404: boolean; onNavigate: (path: string) => void }) {
  return (
        <dl className="grid gap-3 sm:grid-cols-4">
          <div>
            <dt className="text-xs text-muted-foreground">Published</dt>
            <dd className="text-sm">
              {when(data.created_at)} by {data.created_by}
            </dd>
          </div>
          <div>
            <dt className="text-xs text-muted-foreground">Source</dt>
            <dd className="break-all text-sm">{data.source}</dd>
          </div>
          <div>
            <dt className="text-xs text-muted-foreground">Based on</dt>
            <dd className="text-sm">
              {data.base ? (
                <button type="button" className="text-primary hover:underline" onClick={() => onNavigate(`/sites/${encodeURIComponent(site)}/releases/${data.base}`)}>
                  Release {data.base}
                </button>
              ) : (
                "None: published whole"
              )}
            </dd>
          </div>
          <div>
            <dt className="text-xs text-muted-foreground">Contents</dt>
            <dd className="text-sm">
              {data.file_count} files, {formatBytes(data.total_bytes)}
              {has404 ? ", a 404 page" : ""}
            </dd>
          </div>
        </dl>
  );
}

function ReleaseFailure({ site, number, message, onBack }: { site: string; number: number; message?: string; onBack: () => void }) {
  return (
    <div className="space-y-4">
      <PageHeader backLabel={site} onBack={onBack} icon={Globe} title={`Release ${number}`} />
      <Alert variant="destructive">
        <AlertDescription>{message ?? "This release could not be loaded."}</AlertDescription>
      </Alert>
    </div>
  );
}
