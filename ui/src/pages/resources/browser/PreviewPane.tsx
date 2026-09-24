import { useState, type ReactNode } from "react";
import { Folder } from "lucide-react";
import { AuthImg } from "@/components/AuthImg";
import { Button } from "@/components/ui/button";
import { formatBytes } from "@/lib/format";
import { markdownToPlainText } from "@/lib/markdownText";
import { resourceThumbnailSrc } from "@/lib/thumbnailSupport";
import { cn } from "@/lib/utils";
import { useResolvedDark } from "@/stores/theme";
import type { Resource } from "@/api/resources/types";
import { displayPath, type ResourceRoot } from "../scopes";
import { ExtBadge } from "./ExtBadge";
import { plural, shortDate, type Entry } from "./model";

/** The actions the pane offers, which are the page's own. */
export interface PaneActions {
  onOpen: (entry: Entry) => void;
  onDownload: (r: Resource) => void;
  onCopyURI: (r: Resource) => void;
  onRename: () => void;
  onMove: () => void;
  onTag: () => void;
  onDelete: () => void;
  onTagFilter: (tag: string) => void;
}

/**
 * The preview pane (#1872): one file shows its thumbnail and everything about
 * it with Open, Download and Copy URI; one folder its location and contents;
 * several the count, the total size and the bulk actions; nothing selected
 * describes the folder in view.
 */
export function PreviewPane({
  picked,
  root,
  path,
  here,
  canWrite,
  actions,
}: {
  picked: Entry[];
  root: ResourceRoot;
  path: string;
  /** How many files are in the folder in view, at every depth. */
  here: number | undefined;
  canWrite: boolean;
  actions: PaneActions;
}) {
  if (picked.length > 1) return <Many picked={picked} canWrite={canWrite} actions={actions} />;
  const one = picked[0];
  if (one?.kind === "file") return <OneFile r={one.resource} root={root} actions={actions} entry={one} />;
  if (one?.kind === "folder") return <OneFolder entry={one} root={root} canWrite={canWrite} actions={actions} />;
  const name = path ? path.slice(path.lastIndexOf("/") + 1) : root.label;
  return (
    <div className="p-4 text-[12.5px] leading-relaxed text-muted-foreground" data-testid="preview-none">
      <div className="mb-1 font-medium text-foreground">{name}</div>
      {here === undefined ? "" : `${plural(here, "file")} in this folder and the folders inside it. `}
      Select a file to preview it here.
    </div>
  );
}

function Facts({ children }: { children: ReactNode }) {
  return <dl className="grid grid-cols-[78px_1fr] gap-x-2.5 gap-y-1.5 text-[12.5px]">{children}</dl>;
}

function Fact({ label, mono, children }: { label: string; mono?: boolean; children: ReactNode }) {
  return (
    <>
      <dt className="text-muted-foreground">{label}</dt>
      <dd className={cn("min-w-0 [overflow-wrap:anywhere]", mono && "font-mono text-[11.5px]")}>{children}</dd>
    </>
  );
}

function Preview({ r }: { r: Resource }) {
  const isDark = useResolvedDark();
  const [broken, setBroken] = useState(false);
  const src = resourceThumbnailSrc(r, isDark);
  return (
    <div className="m-3 grid aspect-[4/3] place-items-center overflow-hidden rounded-lg border bg-muted">
      {src && !broken ? (
        <AuthImg
          key={src}
          src={src}
          alt=""
          className="h-full w-full object-cover object-top"
          onError={() => setBroken(true)}
          onLoadFailed={() => setBroken(true)}
        />
      ) : (
        <ExtBadge filename={r.filename} className="scale-[2.2]" />
      )}
    </div>
  );
}

function OneFile({
  r,
  entry,
  root,
  actions,
}: {
  r: Resource;
  entry: Entry;
  root: ResourceRoot;
  actions: PaneActions;
}) {
  const tags = r.tags ?? [];
  return (
    <div data-testid="preview-file">
      <Preview r={r} />
      <div className="flex flex-col gap-3 px-3.5 pb-3.5">
        <h2 className="text-[14.5px] font-semibold break-words">{r.display_name}</h2>
        {r.description && <p className="text-muted-foreground">{markdownToPlainText(r.description)}</p>}
        <div className="flex flex-wrap gap-1.5">
          <Button size="xs" onClick={() => actions.onOpen(entry)}>
            Open
          </Button>
          <Button size="xs" variant="outline" onClick={() => actions.onDownload(r)}>
            Download
          </Button>
          <Button size="xs" variant="outline" onClick={() => actions.onCopyURI(r)}>
            Copy URI
          </Button>
        </div>
        <Facts>
          <Fact label="Location" mono>
            {displayPath(root, r.path)}
          </Fact>
          <Fact label="Size">{formatBytes(r.size_bytes)}</Fact>
          <Fact label="Modified">{shortDate(r.updated_at)}</Fact>
          <Fact label="Uploaded by">{r.uploader_email || r.uploader_sub}</Fact>
          <Fact label="URI" mono>
            {r.uri}
          </Fact>
          <Fact label="Tags">
            {tags.length === 0 ? (
              <span className="text-muted-foreground">None</span>
            ) : (
              <span className="flex flex-wrap gap-1">
                {tags.map((t) => (
                  <button
                    key={t}
                    type="button"
                    title={`Show every file tagged ${t}`}
                    className="rounded-full border px-1.5 text-[11px] leading-[17px] text-muted-foreground hover:bg-accent"
                    onClick={() => actions.onTagFilter(t)}
                  >
                    {t}
                  </button>
                ))}
              </span>
            )}
          </Fact>
        </Facts>
      </div>
    </div>
  );
}

function OneFolder({
  entry,
  root,
  canWrite,
  actions,
}: {
  entry: Extract<Entry, { kind: "folder" }>;
  root: ResourceRoot;
  canWrite: boolean;
  actions: PaneActions;
}) {
  return (
    <div data-testid="preview-folder">
      <div className="m-3 grid aspect-[4/3] place-items-center rounded-lg border bg-muted">
        <Folder className="size-16 text-[hsl(217_30%_55%)] dark:text-[hsl(217_30%_68%)]" strokeWidth={0.8} />
      </div>
      <div className="flex flex-col gap-3 px-3.5 pb-3.5">
        <h2 className="text-[14.5px] font-semibold break-words">{entry.name}</h2>
        <Facts>
          <Fact label="Location" mono>
            {displayPath(root, entry.path)}
          </Fact>
          <Fact label="Contains">{plural(entry.count, "file")}</Fact>
          <Fact label="Modified">{shortDate(entry.updatedAt) || "—"}</Fact>
        </Facts>
        <div className="flex flex-wrap gap-1.5">
          <Button size="xs" onClick={() => actions.onOpen(entry)}>
            Open
          </Button>
          {canWrite && (
            <Button size="xs" variant="outline" onClick={actions.onRename}>
              Rename
            </Button>
          )}
        </div>
      </div>
    </div>
  );
}

function Many({ picked, canWrite, actions }: { picked: Entry[]; canWrite: boolean; actions: PaneActions }) {
  const bytes = picked.reduce((n, e) => n + (e.kind === "file" ? e.resource.size_bytes : 0), 0);
  const withFolders = picked.some((e) => e.kind === "folder");
  return (
    <div className="flex flex-col gap-3 px-3.5 pt-4 pb-3.5" data-testid="preview-many">
      <h2 className="text-[14.5px] font-semibold">{picked.length} items selected</h2>
      <p className="text-muted-foreground">
        {formatBytes(bytes)} in files{withFolders ? ", plus the folders’ contents" : ""}.
      </p>
      {canWrite && (
        <div className="flex flex-wrap gap-1.5">
          <Button size="xs" variant="outline" onClick={actions.onMove}>
            Move to...
          </Button>
          <Button size="xs" variant="outline" onClick={actions.onTag}>
            Tag...
          </Button>
          <Button
            size="xs"
            variant="outline"
            className="border-destructive/30 text-destructive hover:bg-destructive/10 hover:text-destructive"
            onClick={actions.onDelete}
          >
            Delete
          </Button>
        </div>
      )}
    </div>
  );
}
