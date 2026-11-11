import { useState } from "react";
import { Folder, Plus } from "lucide-react";
import { useUpdateSite, type Site, type SiteMedia } from "@/api/portal/hooks/sites";
import { useFacets } from "@/api/resources/hooks";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useAuthStore } from "@/stores/auth";
import { Failure } from "./SiteParts";
import { bareAddress, day, errorText, envOpenUrl, orderedEnvironments } from "./siteFormat";

// SiteMediaTab is a site's media folders (#2591): Resources folders it serves
// at an address on every environment, outside its releases. Publish, promote
// and roll back leave them alone, they do not count toward release limits, and
// a file replaced in Resources shows on the site at once. Mounting a folder
// publishes what is in it, so the person adding one must be able to read it;
// the server checks, and refuses a folder they cannot read.

type Library = "global" | "user";

const LIBRARY_LABEL: Record<SiteMedia["scope"], string> = { global: "Global", user: "My Resources", persona: "Persona" };

/** The media list a PATCH sends: every folder but the one removed, or with one added. */
function listOf(media: SiteMedia[]) {
  return media.map(({ prefix, scope, scope_id, folder }) => ({ prefix, scope, scope_id, folder }));
}

export function SiteMediaTab({ site }: { site: Site }) {
  const media = site.media ?? [];
  const update = useUpdateSite(site.name);
  const [adding, setAdding] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const remove = (m: SiteMedia) => {
    setFailure(null);
    update.mutate({ media: listOf(media.filter((x) => x !== m)) }, { onError: (e) => setFailure(errorText(e, "The media folder could not be removed")) });
  };
  return (
    <SectionCard
      title="Media folders"
      action={
        media.length > 0 && (
          <Button size="sm" onClick={() => setAdding(true)}>
            <Plus className="h-4 w-4" aria-hidden />
            Add media folder
          </Button>
        )
      }
    >
      <div className="space-y-3">
        <p className="text-sm text-muted-foreground">
          A media folder serves files from Resources under an address on every environment of this site. It is not part of a release: publish, promote and roll back leave it alone, and it doesn’t
          count toward release limits. Replacing a file in Resources shows on the site at once. A release file at the same path wins over a media file.
        </p>
        {media.length > 0 && <PublicNotice site={site} media={media} />}
        <Failure text={failure} />
        {media.length === 0 ? (
          <div className="rounded border border-dashed p-6 text-center" data-testid="media-empty">
            <p className="font-medium">No media folders yet</p>
            <p className="text-xs text-muted-foreground">Keep images, video and downloads in Resources and serve them here, so a site with thousands of them stays small and rolls back at once.</p>
            <Button size="sm" className="mt-3" onClick={() => setAdding(true)}>
              Add media folder
            </Button>
          </div>
        ) : (
          <MediaTable media={media} busy={update.isPending} onRemove={remove} />
        )}
      </div>
      {adding && <AddMediaDialog site={site} onClose={() => setAdding(false)} />}
    </SectionCard>
  );
}

function PublicNotice({ site, media }: { site: Site; media: SiteMedia[] }) {
  const pub = orderedEnvironments(site.environments).filter((e) => e.public);
  if (pub.length === 0) return null;
  const at = (env: string) => bareAddress(envOpenUrl(site, env));
  const where = pub.flatMap((e) => media.map((m) => `${at(e.name)}${m.prefix}`)).filter((w) => !w.startsWith("/"));
  return (
    <Alert variant="warning" data-testid="media-public">
      <AlertTitle>
        {pub.map((e) => e.name).join(", ")} {pub.length === 1 ? "is" : "are"} public
      </AlertTitle>
      <AlertDescription>
        Everything in these folders opens for anyone{where.length ? ` at ${where.join(" and ")}` : ""}. On private environments only the people who can open them can.
      </AlertDescription>
    </Alert>
  );
}

function MediaTable({ media, busy, onRemove }: { media: SiteMedia[]; busy: boolean; onRemove: (m: SiteMedia) => void }) {
  return (
    <div className="overflow-x-auto" data-testid="media-table">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Address</TableHead>
            <TableHead>Library</TableHead>
            <TableHead>Folder</TableHead>
            <TableHead>Added</TableHead>
            <TableHead />
          </TableRow>
        </TableHeader>
        <TableBody>
          {media.map((m) => (
            <TableRow key={m.prefix + m.folder}>
              <TableCell className="font-mono text-sm">{m.prefix}</TableCell>
              <TableCell>{m.scope === "persona" ? `Persona: ${m.scope_id}` : LIBRARY_LABEL[m.scope]}</TableCell>
              <TableCell className="break-all font-mono text-sm">{m.folder}</TableCell>
              <TableCell>
                <div>{day(m.created_at)}</div>
                <div className="text-xs text-muted-foreground">{m.created_by}</div>
              </TableCell>
              <TableCell className="text-right">
                <Button size="sm" variant="outline" disabled={busy} onClick={() => onRemove(m)}>
                  Remove
                </Button>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
      <p className="mt-2 text-xs text-muted-foreground">Remove takes the address off the site; the folder stays in Resources.</p>
    </div>
  );
}

function AddMediaDialog({ site, onClose }: { site: Site; onClose: () => void }) {
  const update = useUpdateSite(site.name);
  const userId = useAuthStore((s) => s.user?.user_id ?? "");
  const [prefix, setPrefix] = useState("/media/");
  const [library, setLibrary] = useState<Library>("global");
  const [folder, setFolder] = useState("");
  const [failure, setFailure] = useState<string | null>(null);
  const pub = site.environments.filter((e) => e.public).map((e) => e.name);
  const submit = () => {
    setFailure(null);
    const entry = { prefix: prefix.trim(), scope: library, scope_id: library === "user" ? userId : "", folder: folder.trim().replace(/^\/+|\/+$/g, "") };
    update.mutate({ media: [...listOf(site.media ?? []), entry] }, { onSuccess: onClose, onError: (e) => setFailure(errorText(e, "The media folder could not be added")) });
  };
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-h-[90dvh] overflow-y-auto sm:max-w-[620px]">
        <DialogHeader>
          <DialogTitle>Add a media folder to {site.name}</DialogTitle>
          <DialogDescription>Served on every environment of the site</DialogDescription>
        </DialogHeader>
        <div className="min-w-0 space-y-3">
          <div className="space-y-1">
            <Label htmlFor="media-prefix">Address</Label>
            <Input id="media-prefix" className="font-mono" value={prefix} onChange={(e) => setPrefix(e.target.value)} />
            <p className="text-xs text-muted-foreground">Every environment serves the folder here, for example {prefix.trim() || "/media/"}hero.jpg.</p>
          </div>
          <div className="space-y-1">
            <Label htmlFor="media-library">Library</Label>
            <select
              id="media-library"
              className="h-9 w-full rounded-md border bg-background px-3 text-sm"
              value={library}
              onChange={(e) => {
                setLibrary(e.target.value as Library);
                setFolder("");
              }}
            >
              <option value="global">Global</option>
              <option value="user">My Resources</option>
            </select>
          </div>
          <FolderPick library={library} userId={userId} folder={folder} onFolder={setFolder} />
          {pub.length > 0 && (
            <Alert variant="warning" data-testid="media-goes-public">
              <AlertTitle>Files in this folder become public on {pub.join(", ")}</AlertTitle>
              <AlertDescription>
                {pub.join(", ")} {pub.length === 1 ? "is" : "are"} public, so anyone can open them. Adding a folder publishes what is in it, and whatever is added to it later.
              </AlertDescription>
            </Alert>
          )}
          <p className="text-xs text-muted-foreground">
            HTML, SVG, JavaScript and XML files are sent as downloads when opened directly, so a media folder can’t serve pages. Images, video, audio, fonts and PDFs open in the browser, and an SVG still
            shows in an image tag.
          </p>
          <Failure text={failure} testId="media-refused" />
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={!folder.trim() || !prefix.trim() || update.isPending}>
            Add media folder
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// FolderPick lists the library's folders the person can read, from the same
// facets the Resources page draws its tree from; a typed path is checked by the
// server when the folder is added.
function FolderPick({ library, userId, folder, onFolder }: { library: Library; userId: string; folder: string; onFolder: (f: string) => void }) {
  const facets = useFacets(library === "user" ? { scope: "user", scope_id: userId } : { scope: "global" });
  const folders = (facets.data?.folders ?? []).filter((f) => f.path);
  return (
    <div className="space-y-1">
      <Label htmlFor="media-folder">Folder</Label>
      <Input id="media-folder" className="font-mono" placeholder="sites/marketing/media" value={folder} onChange={(e) => onFolder(e.target.value)} />
      {folders.length > 0 && (
        <ul className="max-h-48 overflow-auto rounded border text-sm" data-testid="media-folders">
          {folders.map((f) => (
            <li key={f.path}>
              <button
                type="button"
                className={`flex w-full items-center justify-between gap-2 px-3 py-1.5 text-left hover:bg-muted ${folder === f.path ? "bg-primary/10" : ""}`}
                onClick={() => onFolder(f.path)}
              >
                <span className="flex min-w-0 items-center gap-2 font-mono">
                  <Folder className="h-3.5 w-3.5 shrink-0 text-muted-foreground" aria-hidden />
                  <span className="break-all">{f.path}</span>
                </span>
                <span className="shrink-0 text-xs text-muted-foreground">
                  {f.count.toLocaleString()} {f.count === 1 ? "file" : "files"}
                </span>
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
