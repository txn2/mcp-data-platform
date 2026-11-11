import { useState } from "react";
import { Globe } from "lucide-react";
import { DEV_ENVIRONMENT, useDeleteSite, useSite, useSiteReleases, useUpdateSite, type Site } from "@/api/portal/hooks/sites";
import { PageHeader } from "@/components/patterns/PageHeader";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Textarea } from "@/components/ui/textarea";
import { UserPicker } from "@/components/UserPicker";
import { SiteAccessTab } from "./SiteAccess";
import { SiteDomainsTab } from "./SiteDomains";
import { SiteEnvironmentsTab } from "./SiteEnvironments";
import { SiteFilesTab } from "./SiteFiles";
import { SiteMediaTab } from "./SiteMedia";
import { SiteHistoryTab } from "./SiteHistory";
import { CopySiteDialog } from "./SiteCopy";
import { SiteReleasesTab } from "./SiteReleases";
import { bareAddress, day, errorText } from "./siteFormat";

export type SiteTab = "environments" | "files" | "media" | "releases" | "access" | "domains" | "history";

const TAB_LABELS: Record<SiteTab, string> = {
  environments: "Environments",
  files: "Files",
  media: "Media",
  releases: "Releases",
  access: "Access",
  domains: "Domains",
  history: "History",
};

// SiteDetailPage is one site (#2559). The Access tab shows only to someone who
// manages the site, because who has access is theirs to know.
export function SiteDetailPage({ site, tab, onNavigate }: { site: string; tab: SiteTab; onNavigate: (path: string) => void }) {
  const { data, isLoading, error } = useSite(site);
  const [dialog, setDialog] = useState<SiteDialog>(null);
  const back = () => onNavigate("/sites");
  if (isLoading) return <p className="text-sm text-muted-foreground">Loading…</p>;
  if (error || !data) {
    return (
      <div className="space-y-4">
        <PageHeader backLabel="Sites" onBack={back} icon={Globe} title="Site" />
        <Alert variant="destructive">
          <AlertDescription>{error?.message ?? "This site could not be loaded."}</AlertDescription>
        </Alert>
      </div>
    );
  }
  const tabs = (Object.keys(TAB_LABELS) as SiteTab[]).filter((t) => shows(t, data));
  return (
    <div className="space-y-4">
      <PageHeader backLabel="Sites" onBack={back} icon={Globe} title={data.name} subtitle={data.description} actions={<SiteActions permissions={data.permissions} onOpen={setDialog} />} />
      <Facts site={data} />
      <Tabs value={tab} onValueChange={(t) => onNavigate(`/sites/${encodeURIComponent(data.name)}/${t}`)}>
        <TabsList>
          {tabs.map((t) => (
            <TabsTrigger key={t} value={t}>
              {TAB_LABELS[t]}
            </TabsTrigger>
          ))}
        </TabsList>
      </Tabs>
      <TabBody site={data} tab={tab} onNavigate={onNavigate} />
      <SiteDialogs site={data} dialog={dialog} onClose={() => setDialog(null)} onDeleted={back} />
    </div>
  );
}

// shows says who sees a tab: Access and Media (#2591) are for whoever manages
// the site, and Files (#2590) for whoever can open dev, its tree.
function shows(tab: SiteTab, site: Site): boolean {
  if (tab === "access" || tab === "media") return site.permissions.manage;
  if (tab === "files") return site.permissions.manage || site.environments.some((e) => e.name === DEV_ENVIRONMENT);
  return true;
}

type SiteDialog = "copy" | "edit" | "delete" | null;

function SiteActions({ permissions: p, onOpen }: { permissions: Site["permissions"]; onOpen: (d: SiteDialog) => void }) {
  if (!p.manage) return null;
  return (
    <div className="flex flex-wrap gap-2">
      <Button size="sm" variant="outline" onClick={() => onOpen("copy")}>
        Copy a site
      </Button>
      {p.own && (
        <Button size="sm" variant="outline" onClick={() => onOpen("edit")}>
          Edit
        </Button>
      )}
      {p.delete && (
        <Button size="sm" variant="outline" onClick={() => onOpen("delete")}>
          Delete
        </Button>
      )}
    </div>
  );
}

function TabBody({ site, tab, onNavigate }: { site: Site; tab: SiteTab; onNavigate: (path: string) => void }) {
  if (!shows(tab, site)) return null;
  switch (tab) {
    case "environments":
      return <SiteEnvironmentsTab site={site} onNavigate={onNavigate} />;
    case "files":
      return <SiteFilesTab site={site} onNavigate={onNavigate} />;
    case "media":
      return <SiteMediaTab site={site} />;
    case "releases":
      return <SiteReleasesTab site={site} onNavigate={onNavigate} />;
    case "access":
      return <SiteAccessTab site={site} />;
    case "domains":
      return <SiteDomainsTab site={site} />;
    case "history":
      return <SiteHistoryTab site={site} />;
  }
}

function SiteDialogs({ site, dialog, onClose, onDeleted }: { site: Site; dialog: SiteDialog; onClose: () => void; onDeleted: () => void }) {
  if (dialog === "copy") return <CopySiteDialog site={site} onClose={onClose} />;
  if (dialog === "edit") return <EditSiteDialog site={site} onClose={onClose} />;
  if (dialog === "delete") return <DeleteSiteDialog site={site} onClose={onClose} onDeleted={onDeleted} />;
  return null;
}

function Facts({ site }: { site: Site }) {
  const domains = site.domains.map((d) => d.hostname).join(", ");
  const facts: [string, string, boolean?][] = [
    ["Owner", site.owner_email],
    ["Main address", site.address ? bareAddress(site.address) : "None", true],
    ["Domains", domains || "None"],
    ["Created", `${day(site.created_at)} by ${site.created_by}`],
  ];
  return (
    <dl className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
      {facts.map(([k, v, mono]) => (
        <div key={k}>
          <dt className="text-xs text-muted-foreground">{k}</dt>
          <dd className={`break-words text-sm ${mono ? "font-mono" : ""}`}>{v}</dd>
        </div>
      ))}
    </dl>
  );
}

function EditSiteDialog({ site, onClose }: { site: Site; onClose: () => void }) {
  const update = useUpdateSite(site.name);
  const [description, setDescription] = useState(site.description);
  const [owner, setOwner] = useState(site.owner_email);
  const [failure, setFailure] = useState<string | null>(null);
  const submit = () => {
    setFailure(null);
    update.mutate(
      {
        ...(description !== site.description ? { description } : {}),
        ...(owner.trim() !== site.owner_email ? { owner_email: owner.trim() } : {}),
      },
      { onSuccess: onClose, onError: (e) => setFailure(errorText(e, "The site could not be saved")) },
    );
  };
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Edit site</DialogTitle>
        </DialogHeader>
        <div className="space-y-3">
          <div className="space-y-1">
            <Label htmlFor="edit-name">Name</Label>
            <Input id="edit-name" value={site.name} disabled />
            <p className="text-xs text-muted-foreground">A site's name is part of its address, so it does not change.</p>
          </div>
          <div className="space-y-1">
            <Label htmlFor="edit-description">Description</Label>
            <Textarea id="edit-description" value={description} onChange={(e) => setDescription(e.target.value)} />
          </div>
          <div className="space-y-1">
            <Label>Owner</Label>
            <UserPicker value={owner} onChange={setOwner} />
          </div>
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
          <Button onClick={submit} disabled={update.isPending}>
            Save
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function DeleteSiteDialog({ site, onClose, onDeleted }: { site: Site; onClose: () => void; onDeleted: () => void }) {
  const remove = useDeleteSite(site.name);
  const releases = useSiteReleases(site.name);
  const [typed, setTyped] = useState("");
  const [failure, setFailure] = useState<string | null>(null);
  const addresses = [...new Set([...Object.values(site.addresses ?? {}).filter(Boolean).map(bareAddress), ...site.domains.map((d) => d.hostname)])];
  const submit = () => {
    setFailure(null);
    remove.mutate(undefined, { onSuccess: onDeleted, onError: (e) => setFailure(errorText(e, "The site could not be deleted")) });
  };
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Delete {site.name}?</DialogTitle>
        </DialogHeader>
        <div className="space-y-3">
          <p className="text-sm">
            This takes the site offline and deletes all {releases.data?.releases.length ?? 0} releases. It cannot be undone.
          </p>
          <div>
            <p className="text-sm font-medium">These addresses stop working</p>
            {addresses.map((a) => (
              <p key={a} className="font-mono text-sm">
                {a}
              </p>
            ))}
          </div>
          <div className="space-y-1">
            <Label htmlFor="delete-confirm">Type {site.name} to confirm</Label>
            <Input id="delete-confirm" autoComplete="off" value={typed} onChange={(e) => setTyped(e.target.value)} />
          </div>
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
          <Button variant="destructive" onClick={submit} disabled={typed !== site.name || remove.isPending}>
            Delete site
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
