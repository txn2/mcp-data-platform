import { useMemo, useState } from "react";
import { Globe } from "lucide-react";
import { DEFAULT_ENVIRONMENT, useSite, useSiteRelease, type SiteFile } from "@/api/portal/hooks/sites";
import { PageHeader } from "@/components/patterns/PageHeader";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { formatBytes, hostUrl, hostsOf, when } from "./siteFormat";

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
  if (error || !data) {
    return (
      <div className="space-y-4">
        <PageHeader backLabel={site} onBack={back} icon={Globe} title={`Release ${number}`} />
        <Alert variant="destructive">
          <AlertDescription>{error?.message ?? "This release could not be loaded."}</AlertDescription>
        </Alert>
      </div>
    );
  }
  const openAt = (() => {
    if (!owner) return "";
    if (data.served_in.includes(DEFAULT_ENVIRONMENT) && owner.address) return owner.address;
    for (const env of data.served_in) {
      const host = hostsOf(owner, env)[0];
      if (host) return hostUrl(owner, host.hostname);
    }
    return "";
  })();
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
      <dl className="grid gap-3 sm:grid-cols-3">
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
          <dt className="text-xs text-muted-foreground">Contents</dt>
          <dd className="text-sm">
            {data.file_count} files, {formatBytes(data.total_bytes)}
            {has404 ? ", a 404 page" : ""}
          </dd>
        </div>
      </dl>
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
