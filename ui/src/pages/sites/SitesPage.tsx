import { useState, type ReactNode } from "react";
import { ExternalLink, Globe } from "lucide-react";
import { SITE_NAME_RE, useCreateSite, useSites, type Site, type SiteList } from "@/api/portal/hooks/sites";
import { EmptyState } from "@/components/patterns/EmptyState";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";
import { bareAddress, day, errorText, hostUrl, hostsOf, orderedEnvironments } from "./siteFormat";

// SitesPage lists every site (#2559): who owns it, its own address, and each
// environment with the release it serves, when that last moved and the domains
// added to it.

export function SitesPage({ onNavigate }: { onNavigate: (path: string) => void }) {
  const { data, isLoading, error } = useSites();
  const [creating, setCreating] = useState(false);
  const open = (site: string) => onNavigate(`/sites/${encodeURIComponent(site)}`);
  const newSite = data?.can_create ? (
    <Button size="sm" onClick={() => setCreating(true)}>
      New site
    </Button>
  ) : null;

  return (
    <div className="space-y-4">
      {data && !data.pages_domain && <PreviewsOff admin={data.admin} onNavigate={onNavigate} />}
      <SectionCard title="Sites" action={newSite}>
        <SitesBody list={data} loading={isLoading} error={error} newSite={newSite} onOpen={open} />
      </SectionCard>
      {creating && data && (
        <NewSiteDialog
          pagesDomain={data.pages_domain}
          onClose={() => setCreating(false)}
          onCreated={(name) => {
            setCreating(false);
            open(name);
          }}
        />
      )}
    </div>
  );
}

function SitesBody({
  list,
  loading,
  error,
  newSite,
  onOpen,
}: {
  list?: SiteList;
  loading: boolean;
  error: Error | null;
  newSite: ReactNode;
  onOpen: (site: string) => void;
}) {
  if (error) {
    return (
      <Alert variant="destructive">
        <AlertDescription>{error.message}</AlertDescription>
      </Alert>
    );
  }
  if (loading || !list) return <p className="text-sm text-muted-foreground">Loading…</p>;
  if (list.sites.length === 0) {
    return (
      <EmptyState icon={Globe} action={newSite}>
        <p className="font-medium text-foreground">No sites</p>
        <p className="max-w-md text-xs">A site is a set of web pages this platform serves on its own address. Create one, then upload its files or copy a site that already exists.</p>
      </EmptyState>
    );
  }
  return <SiteTable list={list} onOpen={onOpen} />;
}

function PreviewsOff({ admin, onNavigate }: { admin: boolean; onNavigate: (path: string) => void }) {
  return (
    <Alert data-testid="previews-off">
      <AlertTitle>Previews are off</AlertTitle>
      <AlertDescription className="flex flex-wrap items-center justify-between gap-2">
        {admin ? (
          <>
            <span>This deployment has no pages domain, so a site has no address of its own. A domain added to a site still serves.</span>
            <Button size="sm" variant="outline" onClick={() => onNavigate("/admin/settings/sites")}>
              Set a pages domain
            </Button>
          </>
        ) : (
          <span>A site has no address of its own on this deployment until an administrator sets a pages domain.</span>
        )}
      </AlertDescription>
    </Alert>
  );
}

function SiteTable({ list, onOpen }: { list: SiteList; onOpen: (site: string) => void }) {
  return (
    <div className="overflow-x-auto">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Name</TableHead>
            <TableHead>Owner</TableHead>
            <TableHead>Address</TableHead>
            <TableHead>Environments</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {list.sites.map((s) => (
            <TableRow key={s.id}>
              <TableCell>
                <button type="button" className="font-medium hover:underline" onClick={() => onOpen(s.name)}>
                  {s.name}
                </button>
                <p className="text-xs text-muted-foreground">{s.description}</p>
              </TableCell>
              <TableCell>{s.owner_email}</TableCell>
              <TableCell>
                {s.address ? (
                  <a className="inline-flex items-center gap-1 break-all font-mono text-primary hover:underline" href={s.address} target="_blank" rel="noreferrer">
                    {bareAddress(s.address)}
                    <ExternalLink className="h-3 w-3 shrink-0" aria-hidden />
                  </a>
                ) : (
                  <span className="text-muted-foreground">None</span>
                )}
              </TableCell>
              <TableCell>
                <EnvironmentLines site={s} />
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}

function EnvironmentLines({ site }: { site: Site }) {
  return (
    <div className="space-y-1">
      {orderedEnvironments(site.environments).map((e) => (
        <div key={e.name} className="flex flex-wrap items-center gap-2">
          {e.release > 0 ? (
            <>
              <Badge variant="info">
                {e.name} {e.release}
              </Badge>
              <span className="text-xs text-muted-foreground">{day(e.updated_at)}</span>
            </>
          ) : (
            <Badge variant="muted">{e.name}: no release</Badge>
          )}
          {hostsOf(site, e.name).map((d) => (
            <a key={d.hostname} className="break-all text-xs text-primary hover:underline" href={hostUrl(site, d.hostname)} target="_blank" rel="noreferrer">
              {d.hostname}
            </a>
          ))}
        </div>
      ))}
    </div>
  );
}

function NewSiteDialog({ pagesDomain, onClose, onCreated }: { pagesDomain: string; onClose: () => void; onCreated: (name: string) => void }) {
  const create = useCreateSite();
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [failure, setFailure] = useState<string | null>(null);
  const trimmed = name.trim();
  const valid = SITE_NAME_RE.test(trimmed);
  const submit = () => {
    setFailure(null);
    create.mutate(
      { name: trimmed, description: description.trim() },
      { onSuccess: (s) => onCreated(s.name), onError: (e) => setFailure(errorText(e, "The site could not be created")) },
    );
  };
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>New site</DialogTitle>
        </DialogHeader>
        <div className="space-y-3">
          <div className="space-y-1">
            <Label htmlFor="site-name">Name</Label>
            <Input id="site-name" value={name} onChange={(e) => setName(e.target.value)} autoFocus />
            <p className="text-xs text-muted-foreground">Lowercase letters, digits and single hyphens. It is part of the site's address and does not change.</p>
          </div>
          <div className="space-y-1">
            <Label htmlFor="site-description">Description</Label>
            <Textarea id="site-description" value={description} onChange={(e) => setDescription(e.target.value)} />
          </div>
          {pagesDomain && (
            <div className="space-y-1">
              <Label>Address</Label>
              <p className="font-mono text-sm text-muted-foreground">
                {trimmed || "name"}.{pagesDomain}
              </p>
              <p className="text-xs text-muted-foreground">Only you and the people you add can open it, after they sign in. A system administrator adds its ingress before it answers.</p>
            </div>
          )}
          {failure && (
            <Alert variant="destructive">
              <AlertDescription>{failure}</AlertDescription>
            </Alert>
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={!valid || create.isPending}>
            Create site
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
