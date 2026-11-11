import { useRef, useState } from "react";
import { Upload } from "lucide-react";
import type { Site } from "@/api/portal/hooks/sites";
import { useDeleteSiteFile, useUploadSiteFiles, type SitePublished } from "@/api/portal/hooks/siteFiles";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { Failure } from "./SiteParts";
import { newPathProblem } from "./siteFileFormat";
import { errorText, formatBytes } from "./siteFormat";

// The Files tab's dialogs (#2590): a new file, an upload into a folder and a
// delete. Each is a publish to dev from the release the tab read, so it is an
// ordinary release, and one made against an older release is refused.

export function NewFileDialog({ folder, taken, onCreate, onClose }: { folder: string; taken: (p: string) => boolean; onCreate: (path: string) => void; onClose: () => void }) {
  const [path, setPath] = useState(folder);
  const trimmed = path.trim();
  const problem = newPathProblem(trimmed, taken);
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>New file</DialogTitle>
        </DialogHeader>
        <div className="space-y-1">
          <Label htmlFor="new-file-path">Path</Label>
          <Input id="new-file-path" className="font-mono" value={path} onChange={(e) => setPath(e.target.value)} aria-invalid={!!problem} autoFocus />
          {problem ? (
            <p className="text-xs text-destructive" data-testid="new-file-problem">
              {problem}
            </p>
          ) : (
            <p className="text-xs text-muted-foreground">Folders are made as needed. The file opens empty in the editor and becomes part of the site when you save it.</p>
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={() => onCreate(trimmed)} disabled={!trimmed || !!problem}>
            Create
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

export function UploadDialog({
  site,
  folder,
  base,
  taken,
  maxFileBytes,
  onDone,
  onClose,
}: {
  site: Site;
  folder: string;
  base: number;
  taken: (p: string) => boolean;
  maxFileBytes: number;
  onDone: (out: SitePublished) => void;
  onClose: () => void;
}) {
  const upload = useUploadSiteFiles(site.name);
  const input = useRef<HTMLInputElement>(null);
  const [files, setFiles] = useState<File[]>([]);
  const [note, setNote] = useState("");
  const [failure, setFailure] = useState<string | null>(null);
  const dir = folder.replace(/\/$/, "");
  const at = (f: File) => (dir ? `${dir}/${f.name}` : f.name);
  const tooBig = files.find((f) => f.size > maxFileBytes);
  const add = (list: FileList | null) => {
    if (!list) return;
    const picked = Array.from(list);
    setFiles((cur) => [...cur.filter((f) => !picked.some((p) => p.name === f.name)), ...picked]);
  };
  const submit = () => {
    setFailure(null);
    upload.mutate(
      { files, folder: dir, base, note: note.trim() },
      { onSuccess: onDone, onError: (e) => setFailure(errorText(e, "The files could not be uploaded")) },
    );
  };
  return (
    <Dialog open onOpenChange={(o) => !o && !upload.isPending && onClose()}>
      <DialogContent className="max-h-[90dvh] overflow-y-auto sm:max-w-[620px]">
        <DialogHeader>
          <DialogTitle>{upload.isPending ? "Uploading" : "Upload"} to {dir || "the top folder"}</DialogTitle>
          <DialogDescription>{site.name}</DialogDescription>
        </DialogHeader>
        <div className="space-y-3">
          <div
            className="flex flex-col items-center gap-2 rounded border border-dashed p-6 text-center"
            onDragOver={(e) => e.preventDefault()}
            onDrop={(e) => {
              e.preventDefault();
              add(e.dataTransfer.files);
            }}
          >
            <Upload className="h-6 w-6 text-muted-foreground" aria-hidden />
            <p className="text-sm">Drop files here</p>
            <Button type="button" size="sm" variant="outline" onClick={() => input.current?.click()} disabled={upload.isPending}>
              Choose files
            </Button>
            <input ref={input} type="file" multiple className="hidden" aria-label="Choose files" onChange={(e) => add(e.target.files)} />
            <p className="text-xs text-muted-foreground">Up to {formatBytes(maxFileBytes)} per file. A file with the same name replaces the one in this folder.</p>
          </div>
          {files.length > 0 && (
            <ul className="divide-y rounded border text-sm" data-testid="upload-list">
              {files.map((f) => (
                <li key={f.name} className="flex flex-wrap items-center justify-between gap-2 px-3 py-2">
                  <span className="min-w-0 break-all font-mono">{f.name}</span>
                  <span className="flex items-center gap-3 text-xs text-muted-foreground">
                    {formatBytes(f.size)}
                    <span>{taken(at(f)) ? "Replaces the current file" : "New"}</span>
                    <button type="button" className="text-primary hover:underline" onClick={() => setFiles((cur) => cur.filter((x) => x !== f))} disabled={upload.isPending}>
                      Remove
                    </button>
                  </span>
                </li>
              ))}
            </ul>
          )}
          {tooBig && <Failure text={`${tooBig.name} is larger than the ${formatBytes(maxFileBytes)} a file may be.`} />}
          <WhatChanged value={note} onChange={setNote} />
          <p className="text-xs text-muted-foreground">
            {upload.isPending
              ? `dev keeps serving release ${base} until every file is stored.`
              : `Uploading makes release ${base + 1} from release ${base}, and dev serves it at once. Other environments don’t change.`}
          </p>
          <Failure text={failure} />
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose} disabled={upload.isPending}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={files.length === 0 || !!tooBig || upload.isPending}>
            {upload.isPending ? "Uploading…" : `Upload ${files.length} ${files.length === 1 ? "file" : "files"}`}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

export function DeleteFileDialog({ site, path, base, onDone, onClose }: { site: Site; path: string; base: number; onDone: (out: SitePublished) => void; onClose: () => void }) {
  const remove = useDeleteSiteFile(site.name);
  const [note, setNote] = useState("");
  const [failure, setFailure] = useState<string | null>(null);
  const submit = () => {
    setFailure(null);
    remove.mutate({ path, base, note: note.trim() }, { onSuccess: onDone, onError: (e) => setFailure(errorText(e, "The file could not be deleted")) });
  };
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle className="break-all">Delete {path}?</DialogTitle>
        </DialogHeader>
        <div className="space-y-3">
          <p className="text-sm">
            This makes release {base + 1} without the file, and dev serves it at once. Roll back to release {base} to bring it back.
          </p>
          <WhatChanged value={note} onChange={setNote} />
          <Failure text={failure} />
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="destructive" onClick={submit} disabled={remove.isPending}>
            Delete
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/** WhatChanged is the question every save of a site's files asks, as the asset editor does. */
export function WhatChanged({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  return (
    <div className="space-y-1">
      <Label htmlFor="what-changed">What changed?</Label>
      <Textarea id="what-changed" rows={2} placeholder="Describe your changes (optional)" value={value} onChange={(e) => onChange(e.target.value)} />
    </div>
  );
}
