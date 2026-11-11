import { useMemo, useState } from "react";
import { ExternalLink, File, FileImage, Folder, Plus, Upload } from "lucide-react";
import { DEV_ENVIRONMENT, useSites, type Site, type SiteFile } from "@/api/portal/hooks/sites";
import { useSiteFiles, type SitePublished } from "@/api/portal/hooks/siteFiles";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { DeleteFileDialog, NewFileDialog, UploadDialog } from "./SiteFileDialogs";
import { PublishDialog } from "./SitePublish";
import { envAddress, formatBytes } from "./siteFormat";
import { filePagePath, folderEntries, isRendered, isText } from "./siteFileFormat";

// SiteFilesTab is dev's files (#2590): the tree a person opens a file from to
// fix it by hand. dev is the only environment publish, patch and edits change,
// so it is the only tree shown; stage and prod move by Promote and Roll back.
// Someone dev is shared with reads it; the people who manage the site change it.

type FilesDialog = { kind: "new" } | { kind: "upload" } | { kind: "delete"; path: string } | { kind: "publish" } | null;

export function SiteFilesTab({ site, onNavigate }: { site: Site; onNavigate: (path: string) => void }) {
  const [dialog, setDialog] = useState<FilesDialog>(null);
  const release = site.environments.find((e) => e.name === DEV_ENVIRONMENT)?.release ?? 0;
  if (release === 0) return <NothingYet site={site} manage={site.permissions.manage} dialog={dialog} setDialog={setDialog} onNavigate={onNavigate} />;
  return <FilesBrowser site={site} devRelease={release} onNavigate={onNavigate} />;
}

/** useTree is dev's whole tree, read once, and what the tab reads off it. */
function useTree(site: Site, devRelease: number) {
  const all = useSiteFiles(site.name, "", true);
  const files = useMemo(() => all.data?.files ?? [], [all.data]);
  return {
    all,
    files,
    release: all.data?.version ?? devRelease,
    total: all.data?.total ?? 0,
    truncated: !!all.data?.truncated,
    taken: (p: string) => files.some((f) => f.path === p),
  };
}

function FilesBrowser({ site, devRelease, onNavigate }: { site: Site; devRelease: number; onNavigate: (path: string) => void }) {
  const manage = site.permissions.manage;
  const [folder, setFolder] = useState("");
  const [filter, setFilter] = useState("");
  const [dialog, setDialog] = useState<FilesDialog>(null);
  const [saved, setSaved] = useState<{ path: string; release: number } | null>(null);
  const tree = useTree(site, devRelease);
  const open = (path: string) => onNavigate(filePagePath(site.name, path));
  return (
    <>
      {saved && <DeletedBanner site={site} saved={saved} onNavigate={onNavigate} />}
      <SectionCard
        title={<Crumbs folder={folder} onFolder={setFolder} />}
        action={<HeaderActions site={site} manage={manage} filter={filter} onFilter={setFilter} onAct={setDialog} />}
      >
        <div className="space-y-3">
          <About site={site} release={tree.release} rendered={isRendered(tree.files)} manage={manage} />
          <FilesBody
            tree={tree}
            folder={folder}
            filter={filter.trim()}
            manage={manage}
            onFolder={setFolder}
            onOpen={open}
            onDelete={(path) => setDialog({ kind: "delete", path })}
            onReplace={() => setDialog({ kind: "upload" })}
          />
          {!manage && <p className="text-xs text-muted-foreground">You can view this site. People who manage it can change its files.</p>}
        </div>
      </SectionCard>
      <FilesDialogs
        site={site}
        dialog={dialog}
        folder={folder}
        release={tree.release}
        taken={tree.taken}
        onClose={() => setDialog(null)}
        onDeleted={(path, out) => {
          setDialog(null);
          setSaved({ path, release: out.release.number });
        }}
        onNavigate={onNavigate}
      />
    </>
  );
}

function FilesBody({
  tree,
  ...rest
}: {
  tree: ReturnType<typeof useTree>;
  folder: string;
  filter: string;
  manage: boolean;
  onFolder: (f: string) => void;
  onOpen: (path: string) => void;
  onDelete: (path: string) => void;
  onReplace: () => void;
}) {
  if (tree.all.error) return <LoadFailure onRetry={() => void tree.all.refetch()} />;
  if (tree.all.isLoading) return <p className="text-sm text-muted-foreground">Loading…</p>;
  return <FileTable files={tree.files} total={tree.total} truncated={tree.truncated} {...rest} />;
}

function FilesDialogs({
  site,
  dialog,
  folder,
  release,
  taken,
  onClose,
  onDeleted,
  onNavigate,
}: {
  site: Site;
  dialog: FilesDialog;
  folder: string;
  release: number;
  taken: (p: string) => boolean;
  onClose: () => void;
  onDeleted: (path: string, out: SitePublished) => void;
  onNavigate: (path: string) => void;
}) {
  const maxFileBytes = useSites().data?.limits.max_file_bytes ?? 64 << 20;
  switch (dialog?.kind) {
    case "new":
      return <NewFileDialog folder={folder} taken={taken} onClose={onClose} onCreate={(p) => onNavigate(filePagePath(site.name, p, true))} />;
    case "upload":
      return <UploadDialog site={site} folder={folder} base={release} taken={taken} maxFileBytes={maxFileBytes} onClose={onClose} onDone={onClose} />;
    case "delete":
      return <DeleteFileDialog site={site} path={dialog.path} base={release} onClose={onClose} onDone={(out) => onDeleted(dialog.path, out)} />;
    default:
      return null;
  }
}

function NothingYet({ site, manage, dialog, setDialog, onNavigate }: { site: Site; manage: boolean; dialog: FilesDialog; setDialog: (d: FilesDialog) => void; onNavigate: (path: string) => void }) {
  return (
    <SectionCard title="Files">
      <div className="rounded border border-dashed p-6 text-center" data-testid="files-empty">
        <p className="font-medium">Nothing has been published to dev yet</p>
        <p className="text-xs text-muted-foreground">A site’s files show here once its first release is published.</p>
        {manage && (
          <Button size="sm" className="mt-3" onClick={() => setDialog({ kind: "publish" })}>
            Publish
          </Button>
        )}
      </div>
      {dialog?.kind === "publish" && <PublishDialog site={site} onClose={() => setDialog(null)} onNavigate={onNavigate} />}
    </SectionCard>
  );
}

function Crumbs({ folder, onFolder }: { folder: string; onFolder: (f: string) => void }) {
  const parts = folder.split("/").filter(Boolean);
  return (
    <span className="flex flex-wrap items-center gap-1">
      <button type="button" className={parts.length ? "text-primary hover:underline" : ""} onClick={() => onFolder("")} disabled={parts.length === 0}>
        Files
      </button>
      {parts.map((p, i) => (
        <span key={i} className="flex items-center gap-1">
          <span className="text-muted-foreground">/</span>
          <button type="button" className={i < parts.length - 1 ? "text-primary hover:underline" : ""} onClick={() => onFolder(`${parts.slice(0, i + 1).join("/")}/`)}>
            {p}
          </button>
        </span>
      ))}
    </span>
  );
}

function HeaderActions({ site, manage, filter, onFilter, onAct }: { site: Site; manage: boolean; filter: string; onFilter: (f: string) => void; onAct: (d: FilesDialog) => void }) {
  const address = envAddress(site, DEV_ENVIRONMENT);
  return (
    <div className="flex flex-wrap items-center gap-2">
      <Input className="h-8 w-44" placeholder="Filter by path" aria-label="Filter files by path" value={filter} onChange={(e) => onFilter(e.target.value)} />
      {manage && (
        <>
          <Button size="sm" variant="outline" onClick={() => onAct({ kind: "new" })}>
            <Plus className="h-4 w-4" aria-hidden />
            New file
          </Button>
          <Button size="sm" variant="outline" onClick={() => onAct({ kind: "upload" })}>
            <Upload className="h-4 w-4" aria-hidden />
            Upload
          </Button>
        </>
      )}
      {address && (
        <Button size="sm" variant="outline" asChild>
          <a href={address} target="_blank" rel="noreferrer">
            Open site
            <ExternalLink className="h-3 w-3" aria-hidden />
          </a>
        </Button>
      )}
    </div>
  );
}

function About({ site, release, rendered, manage }: { site: Site; release: number; rendered: boolean; manage: boolean }) {
  let text = `dev serves release ${release}. Saving a change makes release ${release + 1}, and dev serves it at once. Other environments change only when you promote.`;
  if (!manage) text = `dev serves release ${release}. dev is shared with you, so you can read its files. People who manage the site change them.`;
  else if (rendered) text = "These are the source files. The pages people see are rendered from them, and a save renders again only the pages that read what changed.";
  return (
    <div className="flex flex-wrap items-start gap-2">
      <Badge variant="info">dev · release {release}</Badge>
      {rendered && <Badge variant="muted">Rendered</Badge>}
      <p className="min-w-0 flex-1 text-sm text-muted-foreground" data-testid="files-about">
        {text}
        <span className="sr-only"> {site.name}</span>
      </p>
    </div>
  );
}

function LoadFailure({ onRetry }: { onRetry: () => void }) {
  return (
    <Alert variant="destructive" data-testid="files-error">
      <AlertTitle>The files could not be loaded</AlertTitle>
      <AlertDescription className="flex flex-wrap items-center justify-between gap-2">
        <span>The server did not answer. Nothing has changed on the site.</span>
        <Button size="sm" variant="outline" onClick={onRetry}>
          Try again
        </Button>
      </AlertDescription>
    </Alert>
  );
}

function DeletedBanner({ site, saved, onNavigate }: { site: Site; saved: { path: string; release: number }; onNavigate: (path: string) => void }) {
  const address = envAddress(site, DEV_ENVIRONMENT);
  return (
    <Alert variant="success" data-testid="file-deleted">
      <AlertTitle className="break-all">{saved.path} was deleted</AlertTitle>
      <AlertDescription className="flex flex-wrap items-center justify-between gap-2">
        <span>
          Saved as release {saved.release}, and dev serves it now. Roll back to release {saved.release - 1} to restore the file.
        </span>
        <span className="flex gap-2">
          {address && (
            <Button size="sm" variant="outline" asChild>
              <a href={address} target="_blank" rel="noreferrer">
                Open site
              </a>
            </Button>
          )}
          <Button size="sm" variant="outline" onClick={() => onNavigate(`/sites/${encodeURIComponent(site.name)}/environments`)}>
            Promote
          </Button>
        </span>
      </AlertDescription>
    </Alert>
  );
}

function FileTable({
  files,
  folder,
  filter,
  total,
  truncated,
  manage,
  onFolder,
  onOpen,
  onDelete,
  onReplace,
}: {
  files: SiteFile[];
  folder: string;
  filter: string;
  total: number;
  truncated: boolean;
  manage: boolean;
  onFolder: (f: string) => void;
  onOpen: (path: string) => void;
  onDelete: (path: string) => void;
  onReplace: () => void;
}) {
  const entries = folderEntries(files, folder, filter);
  const counted = filter
    ? `${entries.length} of ${total.toLocaleString()} files match “${filter}”`
    : `${entries.length} ${entries.length === 1 ? "item" : "items"} in this folder, ${total.toLocaleString()} files in the site`;
  return (
    <div className="space-y-2">
      <div className="overflow-x-auto">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Name</TableHead>
              <TableHead>Type</TableHead>
              <TableHead>Size</TableHead>
              {manage && <TableHead className="text-right">Actions</TableHead>}
            </TableRow>
          </TableHeader>
          <TableBody>
            {entries.map((e) =>
              e.kind === "folder" ? (
                <TableRow key={e.path} className="cursor-pointer" onClick={() => onFolder(e.path)} data-testid={`folder-${e.path}`}>
                  <TableCell>
                    <span className="flex items-center gap-2">
                      <Folder className="h-4 w-4 shrink-0 text-muted-foreground" aria-hidden />
                      <span className="font-medium">{e.name}</span>
                    </span>
                  </TableCell>
                  <TableCell className="text-muted-foreground">Folder</TableCell>
                  <TableCell className="text-muted-foreground">
                    {e.count.toLocaleString()} {e.count === 1 ? "file" : "files"}
                  </TableCell>
                  {manage && <TableCell />}
                </TableRow>
              ) : (
                <FileRow key={e.path} file={e.file} name={filter ? e.path : e.name} manage={manage} onOpen={onOpen} onDelete={onDelete} onReplace={onReplace} />
              ),
            )}
          </TableBody>
        </Table>
      </div>
      <p className="text-xs text-muted-foreground" data-testid="files-count">
        {counted}
        {truncated && ". Only the first 1,000 files were read; filter by path to reach the rest."}
      </p>
    </div>
  );
}

function FileRow({ file: f, name, manage, onOpen, onDelete, onReplace }: { file: SiteFile; name: string; manage: boolean; onOpen: (p: string) => void; onDelete: (p: string) => void; onReplace: () => void }) {
  const text = isText(f);
  const Icon = text ? File : FileImage;
  return (
    <TableRow className="cursor-pointer" onClick={() => onOpen(f.path)} data-testid={`file-${f.path}`}>
      <TableCell>
        <span className="flex min-w-0 items-center gap-2">
          <Icon className="h-4 w-4 shrink-0 text-muted-foreground" aria-hidden />
          <span className="min-w-0">
            <span className="block break-all font-mono text-sm">{name}</span>
            {f.path === "_redirects" && <span className="block text-xs text-muted-foreground">The redirect map</span>}
            {f.path === "404.html" && <span className="block text-xs text-muted-foreground">The not-found page</span>}
          </span>
        </span>
      </TableCell>
      <TableCell className="text-xs">{f.content_type.split(";")[0]}</TableCell>
      <TableCell className="text-xs">{formatBytes(f.size)}</TableCell>
      {manage && (
        <TableCell className="text-right" onClick={(e) => e.stopPropagation()}>
          <span className="inline-flex gap-2">
            {!text && (
              <Button size="sm" variant="outline" onClick={onReplace}>
                Replace
              </Button>
            )}
            {f.path !== "_redirects" && (
              <Button size="sm" variant="outline" onClick={() => onDelete(f.path)}>
                Delete
              </Button>
            )}
          </span>
        </TableCell>
      )}
    </TableRow>
  );
}
