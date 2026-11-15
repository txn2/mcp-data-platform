import { lazy, Suspense, useEffect, useState } from "react";
import { ArrowLeft, ExternalLink } from "lucide-react";
import { DEV_ENVIRONMENT, useSite, useSiteReleases, useSites, type Site, type SiteRelease } from "@/api/portal/hooks/sites";
import { useSaveSiteFile, useSiteFileText } from "@/api/portal/hooks/siteFiles";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { useUnsavedChangesGuard } from "@/lib/navigationGuard";
import { DeleteFileDialog, UploadDialog } from "./SiteFileDialogs";
import { DrawnView, SiteFileViewer, ViewSourceTabs, drawable, typeInRefusal } from "./SiteFileView";
import { CompareDialog, NoticeBanner, SaveDialog, noticeOf, type Loaded, type Notice } from "./SiteFileSave";
import { Failure } from "./SiteParts";
import { isTemplate } from "./siteFileFormat";
import { envAddress } from "./siteFormat";

const SourceEditor = lazy(() => import("@/components/SourceEditor").then((m) => ({ default: m.SourceEditor })));

// SiteFilePage is one of a site's files in dev, opened to fix by hand (#2590).
// Saving is a publish to dev from the release the editor read, the method an
// agent's patch goes through, so a hand edit is an ordinary release with the
// same checks, lineage and rollback. A site is never rendered inside the
// portal's origin: Open page goes to the site's own address in a new tab.

type Dialogs = "save" | "delete" | "leave" | "compare" | null;

const typeFor = (path: string) => (/\.(html?|md)$/.test(path) ? "text/html" : "text/plain");

export function SiteFilePage({ site: name, path, fresh, onNavigate }: { site: string; path: string; fresh: boolean; onNavigate: (path: string) => void }) {
  const { data: site } = useSite(name);
  const text = useSiteFileText(name, path, !fresh);
  const back = () => onNavigate(`/sites/${encodeURIComponent(name)}/files`);
  if (!site) return <p className="text-sm text-muted-foreground">Loading…</p>;
  if (fresh) {
    const base = site.environments.find((e) => e.name === DEV_ENVIRONMENT)?.release ?? 0;
    return <Editor site={site} path={path} start={{ base, original: "", contentType: typeFor(path) }} fresh onBack={back} onNavigate={onNavigate} />;
  }
  if (text.isLoading) return <p className="text-sm text-muted-foreground">Loading…</p>;
  if (text.error) {
    const binary = /not text/i.test(text.error.message);
    return (
      <div className="space-y-4">
        <Header site={site} path={path} dirty={false} meta="" onBack={back} />
        {binary ? <NotText site={site} path={path} contentType={typeInRefusal(text.error.message)} onBack={back} /> : <Failure text={text.error.message} />}
      </div>
    );
  }
  return <Editor key={text.data!.release} site={site} path={path} start={{ base: text.data!.release, original: text.data!.body, contentType: text.data!.content_type }} onBack={back} onNavigate={onNavigate} />;
}

/** useEditor is the editor's state and what each banner's button does. */
function useEditor(site: Site, path: string, start: Loaded) {
  const saveOnTop = useSaveSiteFile(site.name);
  const [loaded, setLoaded] = useState<Loaded>(start);
  const [draft, setDraft] = useState(start.original);
  const [notice, setNotice] = useState<Notice>(null);
  const [dialog, setDialog] = useState<Dialogs>(null);
  const [failure, setFailure] = useState<string | null>(null);
  const settle = (n: Notice | string) => (typeof n === "string" ? setFailure(n) : setNotice(n));
  const copy = () => void navigator.clipboard?.writeText(draft).catch(() => setFailure("Your browser did not allow copying. Select the text and copy it."));
  const saved = (release: number) => {
    setDialog(null);
    setLoaded((l) => ({ ...l, base: release, original: draft }));
    setNotice({ kind: "saved", release });
  };
  const refused = (n: Notice) => {
    setDialog(null);
    setNotice(n);
  };
  const onTop = () => {
    if (notice?.kind !== "moved") return;
    const on = { ...loaded, base: notice.release };
    saveOnTop.mutate(
      { path, content: draft, base: notice.release },
      { onSuccess: (out) => saved(out.release.number), onError: (e) => void noticeOf(site, path, on, e).then(settle) },
    );
  };
  const load = () => {
    if (notice?.kind !== "changed") return;
    setLoaded((l) => ({ ...l, base: notice.release, original: notice.theirs }));
    setDraft(notice.theirs);
    setNotice(null);
    setDialog(null);
  };
  return { loaded, draft, setDraft, notice, dialog, setDialog, failure, copy, saved, refused, onTop, load };
}

function Editor({ site, path, start, fresh = false, onBack, onNavigate }: { site: Site; path: string; start: Loaded; fresh?: boolean; onBack: () => void; onNavigate: (path: string) => void }) {
  const manage = site.permissions.manage;
  const releases = useSiteReleases(site.name).data?.releases;
  const ed = useEditor(site, path, start);
  const [leaving, setLeaving] = useState(false);
  const dirty = manage && !leaving && (fresh || ed.draft !== ed.loaded.original);
  useUnsavedChangesGuard(dirty);
  useEffect(() => {
    if (leaving) onBack();
  }, [leaving]); // eslint-disable-line react-hooks/exhaustive-deps -- leave once the guard is released
  const openPage = pageUrl(site, path);
  return (
    <div className="space-y-4">
      <Header
        site={site}
        path={path}
        dirty={dirty}
        meta={`${site.name} · ${ed.loaded.contentType.split(";")[0]} · dev, release ${ed.loaded.base}`}
        openPage={openPage}
        onBack={() => (dirty ? ed.setDialog("leave") : onBack())}
        actions={
          manage && (
            <EditorActions dirty={dirty} fresh={fresh} onDelete={() => ed.setDialog("delete")} onDiscard={() => ed.setDraft(ed.loaded.original)} onSave={() => ed.setDialog("save")} />
          )
        }
      />
      <NoticeBanner
        notice={ed.notice}
        releases={releases}
        onSaveOnTop={ed.onTop}
        onCompare={() => ed.setDialog("compare")}
        onCopy={ed.copy}
        onLoad={ed.load}
        openPage={openPage}
        onPromote={() => onNavigate(`/sites/${encodeURIComponent(site.name)}/environments`)}
      />
      <Failure text={ed.failure} />
      <EditorBody manage={manage} path={path} ed={ed} />
      <EditorDialogs site={site} path={path} ed={ed} releases={releases} onLeave={() => setLeaving(true)} />
    </div>
  );
}

function EditorBody({ manage, path, ed }: { manage: boolean; path: string; ed: ReturnType<typeof useEditor> }) {
  // A text kind the viewer draws (#2629) opens on View for someone who can
  // only read it, and on Source for someone who manages the site, whose View
  // draws the draft, unsaved edits included.
  const drawn = drawable(ed.loaded.contentType, path);
  const [view, setView] = useState<"view" | "source">(manage ? "source" : "view");
  if (drawn) {
    return (
      <div className="space-y-2">
        <ViewSourceTabs view={view} onChange={setView} />
        {view === "view" ? <DrawnView body={ed.draft} contentType={ed.loaded.contentType} path={path} /> : manage ? <SourceBody path={path} ed={ed} /> : <ReadOnly body={ed.draft} />}
      </div>
    );
  }
  if (!manage) return <ReadOnly body={ed.draft} />;
  return <SourceBody path={path} ed={ed} />;
}

function SourceBody({ path, ed }: { path: string; ed: ReturnType<typeof useEditor> }) {
  return (
    <>
      {isTemplate(path) && (
        <Alert>
          <AlertDescription>Every page that reads this file is rendered again when you save. Save shows how many pages change before anything is stored.</AlertDescription>
        </Alert>
      )}
      <Suspense fallback={<p className="text-sm text-muted-foreground">Loading the editor…</p>}>
        <div className="min-w-0 overflow-hidden rounded border" data-testid="file-editor">
          <SourceEditor content={ed.draft} contentType={ed.loaded.contentType} fileName={path} onChange={ed.setDraft} />
        </div>
      </Suspense>
    </>
  );
}

function EditorDialogs({ site, path, ed, releases, onLeave }: { site: Site; path: string; ed: ReturnType<typeof useEditor>; releases?: SiteRelease[]; onLeave: () => void }) {
  const close = () => ed.setDialog(null);
  switch (ed.dialog) {
    case "save":
      return <SaveDialog site={site} path={path} loaded={ed.loaded} draft={ed.draft} onSaved={ed.saved} onRefused={ed.refused} onClose={close} />;
    case "delete":
      return <DeleteFileDialog site={site} path={path} base={ed.loaded.base} onDone={onLeave} onClose={close} />;
    case "leave":
      return <LeaveDialog path={path} onKeep={close} onDiscard={onLeave} />;
    case "compare":
      if (ed.notice?.kind !== "changed") return null;
      return (
        <CompareDialog path={path} loaded={ed.loaded} draft={ed.draft} theirs={ed.notice.theirs} release={ed.notice.release} releases={releases} onCopy={ed.copy} onLoad={ed.load} onClose={close} />
      );
    default:
      return null;
  }
}

/** The page a file is served as on dev, "" for a file that is not a page or a site with no address. */
function pageUrl(site: Site, path: string): string {
  const address = envAddress(site, DEV_ENVIRONMENT);
  if (!address || path.startsWith("_")) return "";
  return `${address}/${path.replace(/(^|\/)index\.html$/, "$1")}`;
}

function Header({
  site,
  path,
  dirty,
  meta,
  openPage = "",
  onBack,
  actions,
}: {
  site: Site;
  path: string;
  dirty: boolean;
  meta: string;
  openPage?: string;
  onBack: () => void;
  actions?: React.ReactNode;
}) {
  return (
    <div className="space-y-2">
      <button type="button" className="inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground" onClick={onBack}>
        <ArrowLeft className="h-4 w-4" aria-hidden />
        {site.name} files
      </button>
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div className="min-w-0 space-y-1">
          <h1 className="flex flex-wrap items-center gap-2 break-all font-mono text-lg font-semibold">
            {path}
            {path.startsWith("_layouts/") && <Badge variant="muted">Layout</Badge>}
            {dirty && <Badge variant="warning">Unsaved changes</Badge>}
          </h1>
          <p className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
            {meta}
            {openPage && (
              <a className="inline-flex items-center gap-1 text-primary hover:underline" href={openPage} target="_blank" rel="noreferrer">
                Open page
                <ExternalLink className="h-3 w-3" aria-hidden />
              </a>
            )}
          </p>
        </div>
        {actions}
      </div>
    </div>
  );
}

function EditorActions({ dirty, fresh, onDelete, onDiscard, onSave }: { dirty: boolean; fresh: boolean; onDelete: () => void; onDiscard: () => void; onSave: () => void }) {
  return (
    <div className="flex flex-wrap gap-2">
      {!fresh && (
        <Button size="sm" variant="outline" onClick={onDelete}>
          Delete
        </Button>
      )}
      {dirty && !fresh && (
        <Button size="sm" variant="outline" onClick={onDiscard}>
          Discard
        </Button>
      )}
      <Button size="sm" onClick={onSave} disabled={!dirty}>
        Save
      </Button>
    </div>
  );
}

function ReadOnly({ body }: { body: string }) {
  return (
    <div className="space-y-2">
      <p className="text-xs text-muted-foreground">You can view this file. People who manage the site can change it.</p>
      <pre className="max-h-[70dvh] overflow-auto rounded border p-3 font-mono text-xs" data-testid="file-readonly">
        {body.split("\n").map((line, i) => (
          <div key={i} className="flex">
            <span className="w-10 shrink-0 select-none pr-3 text-right text-muted-foreground">{i + 1}</span>
            <span className="whitespace-pre">{line}</span>
          </div>
        ))}
      </pre>
    </div>
  );
}

// NotText is a file that is not text, drawn by the portal's viewer (#2629):
// people who manage the site can replace it; everyone who can read it can
// download it.
function NotText({ site, path, contentType, onBack }: { site: Site; path: string; contentType: string; onBack: () => void }) {
  const [replacing, setReplacing] = useState(false);
  const maxFileBytes = useSites().data?.limits.max_file_bytes ?? 64 << 20;
  const base = site.environments.find((e) => e.name === DEV_ENVIRONMENT)?.release ?? 0;
  const folder = path.includes("/") ? path.slice(0, path.lastIndexOf("/") + 1) : "";
  return (
    <div className="space-y-2" data-testid="file-binary">
      <SiteFileViewer
        site={site.name}
        path={path}
        contentType={contentType}
        actions={
          site.permissions.manage && (
            <Button size="sm" variant="outline" onClick={() => setReplacing(true)}>
              Replace
            </Button>
          )
        }
      />
      {replacing && <UploadDialog site={site} folder={folder} base={base} taken={(p) => p === path} maxFileBytes={maxFileBytes} onClose={() => setReplacing(false)} onDone={onBack} />}
    </div>
  );
}

function LeaveDialog({ path, onKeep, onDiscard }: { path: string; onKeep: () => void; onDiscard: () => void }) {
  return (
    <Dialog open onOpenChange={(o) => !o && onKeep()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Discard unsaved changes?</DialogTitle>
        </DialogHeader>
        <p className="text-sm">You have unsaved changes to {path}. If you leave, your edits will be lost.</p>
        <DialogFooter>
          <Button variant="outline" onClick={onKeep}>
            Keep editing
          </Button>
          <Button variant="destructive" onClick={onDiscard}>
            Discard
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

