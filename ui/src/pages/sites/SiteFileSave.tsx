import { useEffect, useState } from "react";
import { ApiError } from "@/api/portal/client";
import type { Site, SiteRelease } from "@/api/portal/hooks/sites";
import { readSiteFile, useSaveSiteFile } from "@/api/portal/hooks/siteFiles";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { EditDiff } from "./SiteDiff";
import { WhatChanged } from "./SiteFileDialogs";
import { Failure } from "./SiteParts";
import { isTemplate } from "./siteFileFormat";
import { errorText, when } from "./siteFormat";

// Saving one of a site's files (#2590): the Save dialog with the change and
// "What changed?", the pages a template's save renders again (from a dry run
// that stores nothing), and what a refused save turns into on the editor: dev
// moved on without this file (one click saves on top), someone changed this
// file (never an overwrite), the render failed (naming file and line), or the
// person lost access. Nothing is ever merged automatically.

export type Notice =
  | { kind: "saved"; release: number }
  | { kind: "moved"; release: number }
  | { kind: "changed"; release: number; theirs: string }
  | { kind: "render"; message: string; release: number }
  | { kind: "refused" }
  | null;

/** What the editor holds: the release it read, the text it read there, and its type. */
export interface Loaded {
  base: number;
  original: string;
  contentType: string;
}

/** noticeOf turns a refused save into what the editor says, reading dev again on a conflict. */
export async function noticeOf(site: Site, path: string, loaded: Loaded, err: unknown): Promise<Notice | string> {
  if (!(err instanceof ApiError)) return errorText(err, "The file could not be saved");
  if (err.status === 403) return { kind: "refused" };
  if (err.status === 409) {
    const now = await readSiteFile(site.name, path).catch(() => null);
    if (!now) return { kind: "changed", release: loaded.base + 1, theirs: "" };
    return now.body === loaded.original ? { kind: "moved", release: now.release } : { kind: "changed", release: now.release, theirs: now.body };
  }
  if (err.status === 422) return { kind: "render", message: err.detail, release: loaded.base };
  return errorText(err, "The file could not be saved");
}

export function SaveDialog({
  site,
  path,
  loaded,
  draft,
  onSaved,
  onRefused,
  onClose,
}: {
  site: Site;
  path: string;
  loaded: Loaded;
  draft: string;
  onSaved: (release: number) => void;
  onRefused: (n: Notice) => void;
  onClose: () => void;
}) {
  const save = useSaveSiteFile(site.name);
  const check = useSaveSiteFile(site.name);
  const [note, setNote] = useState("");
  const [failure, setFailure] = useState<string | null>(null);
  const template = isTemplate(path);
  const pages = check.data?.outputs_changed;
  useEffect(() => {
    if (!template) return;
    check.mutate(
      { path, content: draft, base: loaded.base, dry_run: true },
      {
        onError: (e) =>
          void noticeOf(site, path, loaded, e).then((n) => {
            if (typeof n === "string") setFailure(n);
            else onRefused(n);
          }),
      },
    );
  }, []); // eslint-disable-line react-hooks/exhaustive-deps -- one dry run when the dialog opens
  const submit = () => {
    setFailure(null);
    save.mutate(
      { path, content: draft, base: loaded.base, note: note.trim() },
      {
        onSuccess: (out) => onSaved(out.release.number),
        onError: (e) =>
          void noticeOf(site, path, loaded, e).then((n) => {
            if (typeof n === "string") setFailure(n);
            else onRefused(n);
          }),
      },
    );
  };
  return (
    <Dialog open onOpenChange={(o) => !o && !save.isPending && onClose()}>
      <DialogContent className="max-h-[90dvh] overflow-y-auto sm:max-w-3xl">
        <DialogHeader>
          <DialogTitle className="break-all">
            {save.isPending ? "Saving" : "Save"} {path}
          </DialogTitle>
          <DialogDescription>{site.name}</DialogDescription>
        </DialogHeader>
        <div className="min-w-0 space-y-3">
          <p className="text-sm font-medium">Changes</p>
          <EditDiff from={loaded.original} to={draft} />
          {template && <PagesChange checking={check.isPending} pages={pages} />}
          <WhatChanged value={note} onChange={setNote} />
          <p className="text-xs text-muted-foreground">
            {save.isPending
              ? `Making release ${loaded.base + 1}. dev keeps serving release ${loaded.base} until it is stored.`
              : `Saving makes release ${loaded.base + 1} from release ${loaded.base}, and dev serves it at once. Other environments don’t change.`}
          </p>
          <Failure text={failure} />
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose} disabled={save.isPending}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={save.isPending || (template && check.isPending)}>
            {save.isPending ? "Saving…" : "Save"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function PagesChange({ checking, pages }: { checking: boolean; pages?: number }) {
  if (checking || pages === undefined) return <p className="text-sm text-muted-foreground">Checking which pages change…</p>;
  return (
    <Alert data-testid="pages-change">
      <AlertTitle>
        {pages.toLocaleString()} {pages === 1 ? "page changes" : "pages change"} when this is saved
      </AlertTitle>
      <AlertDescription>Every page that reads this file. The render was checked without storing anything, and no other page changes.</AlertDescription>
    </Alert>
  );
}

/** Who made a release and what they said, for a conflict's sentence. */
function madeBy(release: number, releases?: SiteRelease[]): string {
  const r = releases?.find((x) => x.number === release);
  if (!r) return `Release ${release} was saved`;
  return `${r.created_by} saved release ${release} at ${when(r.created_at)}${r.note ? `: “${r.note}”` : ""}`;
}

export function NoticeBanner({
  notice: n,
  releases,
  onSaveOnTop,
  onCompare,
  onCopy,
  onLoad,
  openPage,
  onPromote,
}: {
  notice: Notice;
  releases?: SiteRelease[];
  onSaveOnTop: () => void;
  onCompare: () => void;
  onCopy: () => void;
  onLoad: () => void;
  openPage: string;
  onPromote: () => void;
}) {
  if (!n) return null;
  switch (n.kind) {
    case "saved":
      return (
        <Alert variant="success" data-testid="file-saved">
          <AlertTitle>Saved as release {n.release}</AlertTitle>
          <AlertDescription className="flex flex-wrap items-center justify-between gap-2">
            <span>dev serves it now. Other environments change only when you promote.</span>
            <span className="flex gap-2">
              {openPage && (
                <Button size="sm" variant="outline" asChild>
                  <a href={openPage} target="_blank" rel="noreferrer">
                    Open page
                  </a>
                </Button>
              )}
              <Button size="sm" variant="outline" onClick={onPromote}>
                Promote
              </Button>
            </span>
          </AlertDescription>
        </Alert>
      );
    case "moved":
      return (
        <Alert variant="warning" data-testid="conflict-moved">
          <AlertTitle>dev moved to release {n.release} while you were editing</AlertTitle>
          <AlertDescription className="flex flex-wrap items-center justify-between gap-2">
            <span>{madeBy(n.release, releases)}. It did not change this file, so your changes can be saved on top of it.</span>
            <Button size="sm" onClick={onSaveOnTop}>
              Save on release {n.release}
            </Button>
          </AlertDescription>
        </Alert>
      );
    case "changed":
      return (
        <Alert variant="destructive" data-testid="conflict-changed">
          <AlertTitle>This file changed in release {n.release} while you were editing</AlertTitle>
          <AlertDescription className="flex flex-wrap items-center justify-between gap-2">
            <span>{madeBy(n.release, releases)}. Nothing was saved, and your changes are still here.</span>
            <span className="flex flex-wrap gap-2">
              <Button size="sm" variant="outline" onClick={onCompare}>
                Compare
              </Button>
              <Button size="sm" variant="outline" onClick={onCopy}>
                Copy my version
              </Button>
              <Button size="sm" variant="outline" onClick={onLoad}>
                Load release {n.release}
              </Button>
            </span>
          </AlertDescription>
        </Alert>
      );
    case "render":
      return (
        <Alert variant="destructive" data-testid="render-error">
          <AlertTitle>Not saved: the site failed to render</AlertTitle>
          <AlertDescription>
            {n.message} dev still serves release {n.release}.
          </AlertDescription>
        </Alert>
      );
    case "refused":
      return (
        <Alert variant="destructive" data-testid="access-lost">
          <AlertTitle>You can no longer change this site</AlertTitle>
          <AlertDescription className="flex flex-wrap items-center justify-between gap-2">
            <span>Your access changed while you were editing, so nothing was saved. Your changes are still here so you can copy them.</span>
            <Button size="sm" variant="outline" onClick={onCopy}>
              Copy my version
            </Button>
          </AlertDescription>
        </Alert>
      );
  }
}

export function CompareDialog({
  path,
  loaded,
  draft,
  theirs,
  release,
  releases,
  onCopy,
  onLoad,
  onClose,
}: {
  path: string;
  loaded: Loaded;
  draft: string;
  theirs: string;
  release: number;
  releases?: SiteRelease[];
  onCopy: () => void;
  onLoad: () => void;
  onClose: () => void;
}) {
  const who = releases?.find((r) => r.number === release)?.created_by;
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-h-[90dvh] overflow-y-auto sm:max-w-5xl">
        <DialogHeader>
          <DialogTitle className="break-all">Compare changes to {path}</DialogTitle>
          <DialogDescription>Both changes start from release {loaded.base}.</DialogDescription>
        </DialogHeader>
        <div className="grid min-w-0 gap-4 md:grid-cols-2" data-testid="compare">
          <div className="min-w-0 space-y-2">
            <p className="text-sm font-medium">
              Release {release}
              {who ? `, by ${who}` : ""}
            </p>
            <EditDiff from={loaded.original} to={theirs} />
          </div>
          <div className="min-w-0 space-y-2">
            <p className="text-sm font-medium">Your changes · Not saved</p>
            <EditDiff from={loaded.original} to={draft} />
          </div>
        </div>
        <p className="text-xs text-muted-foreground">
          To keep both, load release {release} and make your change again there. Copy my version puts your whole file on the clipboard first.
        </p>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Close
          </Button>
          <Button variant="outline" onClick={onCopy}>
            Copy my version
          </Button>
          <Button onClick={onLoad}>Load release {release}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
