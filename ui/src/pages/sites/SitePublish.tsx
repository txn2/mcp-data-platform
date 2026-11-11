import { useRef, useState, type DragEvent } from "react";
import { Upload } from "lucide-react";
import { DEV_ENVIRONMENT, usePublishSite, useSites, type BucketSource, type Site, type SiteLimits } from "@/api/portal/hooks/sites";
import { useCatalogConnections } from "@/api/portal/catalog";
import { useResources, useUploadResource } from "@/api/resources/hooks";
import type { Resource } from "@/api/resources/types";
import { useAuthStore } from "@/stores/auth";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { JobDialog, PublishRefused } from "./SitePublishJob";
import { bareAddress, envOpenUrl, errorText, formatBytes } from "./siteFormat";

export const number = new Intl.NumberFormat("en-US");

function limitsText(l?: SiteLimits): string {
  if (!l) return "";
  return `Up to ${number.format(l.max_release_files)} files, ${formatBytes(l.max_file_bytes)} per file, ${formatBytes(l.max_release_bytes)} in all. A _redirects file becomes the redirect map and 404.html the not-found page.`;
}

interface Picked {
  label: string;
  files: File[];
  folder: boolean;
}

// PublishDialog publishes to dev from an upload, an archive in Resources or a
// bucket prefix (#2559, #2593). An upload goes through the managed-resource
// upload first, so there is one publish path. A publish makes a new release
// and always lands in dev, the working copy; other environments move only by
// Promote and Roll back (#2604). The work runs on the server as a job: closing
// the dialog does not stop it, and the Environments tab shows it with Stop.
export function PublishDialog({ site, onClose, onNavigate }: { site: Site; onClose: () => void; onNavigate: (path: string) => void }) {
  const limits = useSites().data?.limits;
  const [form, setForm] = useState<PublishForm>({ source: "upload", picked: null, resource: null, bucket: { connection: "", bucket: "", prefix: "" }, note: "" });
  const flow = usePublishFlow(site.name, form);
  if (flow.jobId) return <JobDialog site={site} jobId={flow.jobId} onClose={onClose} onAgain={flow.reset} onNavigate={onNavigate} />;
  if (flow.failure) return <PublishRefused site={site} message={flow.failure} onClose={onClose} onAgain={flow.reset} />;
  const { refusal, ready } = publishState(form, limits);
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-h-[90dvh] overflow-y-auto sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>Publish to dev</DialogTitle>
          <DialogDescription>{site.name}</DialogDescription>
        </DialogHeader>
        <PublishFields site={site} form={form} onChange={setForm} limits={limits} refusal={refusal} uploading={flow.uploading} />
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={() => void flow.start()} disabled={!ready || flow.busy}>
            Publish to dev
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// publishState is what the form says now: why it cannot go ahead, if so.
function publishState(form: PublishForm, limits: SiteLimits | undefined) {
  const tooBig = limits && form.picked ? oversize(form.picked.files, limits) : null;
  const ready = {
    upload: !!form.picked && !tooBig,
    resources: !!form.resource,
    bucket: !!form.bucket.connection && !!form.bucket.bucket.trim(),
  }[form.source];
  return { refusal: form.source === "upload" ? tooBig : null, ready };
}

interface PublishForm {
  source: "upload" | "resources" | "bucket";
  picked: Picked | null;
  resource: Resource | null;
  bucket: Required<BucketSource>;
  note: string;
}

// usePublishFlow uploads what was picked, starts the publish and follows it.
function usePublishFlow(site: string, form: PublishForm) {
  const publish = usePublishSite(site);
  const upload = useUploadResource();
  const userId = useAuthStore((s) => s.user?.user_id ?? "");
  const [uploading, setUploading] = useState<{ done: number; total: number } | null>(null);
  const [jobId, setJobId] = useState<string | null>(null);
  const [failure, setFailure] = useState<string | null>(null);
  const target = async () => {
    if (form.source === "bucket") return bucketTarget(form.bucket);
    if (form.source === "resources") return resourceTarget(form.resource!);
    return uploadPicked(form.picked!, { site, userId }, upload.mutateAsync, setUploading);
  };
  const start = async () => {
    setFailure(null);
    try {
      const j = await publish.mutateAsync({ note: form.note.trim(), ...(await target()) });
      setJobId(j.id);
    } catch (e) {
      setFailure(errorText(e, "The publish could not be started"));
    } finally {
      setUploading(null);
    }
  };
  const reset = () => {
    setJobId(null);
    setFailure(null);
  };
  return { start, reset, jobId, failure, uploading, busy: !!uploading || publish.isPending };
}

function PublishFields({
  site,
  form,
  onChange,
  limits,
  refusal,
  uploading,
}: {
  site: Site;
  form: PublishForm;
  onChange: (f: PublishForm) => void;
  limits?: SiteLimits;
  refusal?: string | null;
  uploading: { done: number; total: number } | null;
}) {
  const set = (patch: Partial<PublishForm>) => onChange({ ...form, ...patch });
  const devAt = envOpenUrl(site, DEV_ENVIRONMENT);
  const dev = site.environments.find((e) => e.name === DEV_ENVIRONMENT);
  return (
    <div className="space-y-3">
      {refusal && (
        <Alert variant="destructive" data-testid="publish-refused">
          <AlertTitle>Nothing was published</AlertTitle>
          <AlertDescription>{refusal}</AlertDescription>
        </Alert>
      )}
      <Tabs value={form.source} onValueChange={(v) => set({ source: v as PublishForm["source"] })}>
        <TabsList className="h-auto flex-wrap">
          <TabsTrigger value="upload">Upload</TabsTrigger>
          <TabsTrigger value="resources">Archive in Resources</TabsTrigger>
          <TabsTrigger value="bucket">Bucket prefix</TabsTrigger>
        </TabsList>
      </Tabs>
      <SourceFields form={form} set={set} limits={limits} />
      {uploading && (
        <p className="text-sm text-muted-foreground">
          Uploading {uploading.done} of {uploading.total}
        </p>
      )}
      <div className="space-y-1">
        <Label htmlFor="publish-note">What changed</Label>
        <Input id="publish-note" placeholder="What changed" value={form.note} onChange={(e) => set({ note: e.target.value })} />
      </div>
      <DevOnlyNote site={site} devAt={devAt} release={(dev?.release ?? 0) + 1} />
    </div>
  );
}

function SourceFields({ form, set, limits }: { form: PublishForm; set: (patch: Partial<PublishForm>) => void; limits?: SiteLimits }) {
  switch (form.source) {
    case "upload":
      return <UploadDrop picked={form.picked} onPick={(picked) => set({ picked })} limits={limits} />;
    case "resources":
      return <ResourcePicker chosen={form.resource} onChoose={(resource) => set({ resource })} />;
    case "bucket":
      return <BucketFields value={form.bucket} onChange={(bucket) => set({ bucket })} />;
  }
}

// BucketFields names a prefix of a bucket on an S3 connection the person can
// use. The server reads it with the connection's own credentials, inside its
// bucket_prefix, and refuses a connection or prefix the person cannot read.
function BucketFields({ value, onChange }: { value: Required<BucketSource>; onChange: (b: Required<BucketSource>) => void }) {
  const { data, isLoading } = useCatalogConnections();
  const s3 = (data?.connections ?? []).filter((c) => c.kind === "s3");
  return (
    <div className="space-y-3" data-testid="publish-bucket">
      <div className="space-y-1">
        <Label htmlFor="publish-connection">Connection</Label>
        <select
          id="publish-connection"
          className="h-9 w-full rounded-md border bg-background px-3 text-sm"
          value={value.connection}
          onChange={(e) => onChange({ ...value, connection: e.target.value })}
        >
          <option value="">{isLoading ? "Loading…" : s3.length === 0 ? "No S3 connection you can use" : "Choose a connection"}</option>
          {s3.map((c) => (
            <option key={c.name} value={c.name}>
              {c.name} · S3
            </option>
          ))}
        </select>
        <p className="text-xs text-muted-foreground">Only connections you can use are listed. Files are read with the connection’s own credentials.</p>
      </div>
      <div className="grid gap-3 sm:grid-cols-2">
        <div className="space-y-1">
          <Label htmlFor="publish-bucket-name">Bucket</Label>
          <Input id="publish-bucket-name" className="font-mono" value={value.bucket} onChange={(e) => onChange({ ...value, bucket: e.target.value })} />
        </div>
        <div className="space-y-1">
          <Label htmlFor="publish-prefix">Prefix</Label>
          <Input id="publish-prefix" className="font-mono" placeholder="site/build/" value={value.prefix} onChange={(e) => onChange({ ...value, prefix: e.target.value })} />
        </div>
      </div>
    </div>
  );
}

// DevOnlyNote says that a publish changes dev and nothing else, which is what
// keeps an agent's or a person's work away from what people use.
export function DevOnlyNote({ site, devAt, release }: { site: Site; devAt: string; release: number }) {
  const others = site.environments.filter((e) => e.name !== DEV_ENVIRONMENT && e.release > 0);
  return (
    <Alert>
      <AlertTitle>This changes dev only</AlertTitle>
      <AlertDescription>
        dev becomes release {release}
        {devAt ? ` at ${bareAddress(devAt)}` : ""}, for the people who manage this site.{" "}
        {others.length > 0 ? `${others.map((e) => `${e.name} keeps serving release ${e.release}`).join(", ")} until you promote.` : "Promote it when people should use it."}
      </AlertDescription>
    </Alert>
  );
}

function oversize(files: File[], l: SiteLimits): string | null {
  const big = files.find((f) => !isArchive(f) && f.size > l.max_file_bytes);
  if (big) return `${relativePath(big)} is ${formatBytes(big.size)}. One file can be at most ${formatBytes(l.max_file_bytes)}.`;
  const total = files.reduce((n, f) => n + f.size, 0);
  if (total > l.max_release_bytes) return `This is ${formatBytes(total)}. A release can be at most ${formatBytes(l.max_release_bytes)}.`;
  if (files.length > l.max_release_files) return `This is ${number.format(files.length)} files. A release can hold at most ${number.format(l.max_release_files)}.`;
  return null;
}

function isArchive(f: File) {
  return /\.(zip|tgz|tar\.gz)$/i.test(f.name);
}

function relativePath(f: File) {
  return (f as File & { webkitRelativePath?: string }).webkitRelativePath || f.name;
}

function resourceTarget(r: Resource) {
  return { archive: `mcp:resource:${r.id}`, source: r.filename };
}

function bucketTarget(b: Required<BucketSource>) {
  const from: BucketSource = { connection: b.connection, bucket: b.bucket.trim() };
  if (b.prefix.trim()) from.prefix = b.prefix.trim();
  return { from_bucket: from };
}

type Upload = (form: FormData) => Promise<Resource>;

interface UploadTarget {
  site: string;
  userId: string;
}

// uploadPicked puts a zip, or each file of a folder, in the person's own
// managed resources, and says what to publish from.
async function uploadPicked(p: Picked, { site, userId }: UploadTarget, upload: Upload, progress: (s: { done: number; total: number }) => void) {
  const folderPath = `sites/${site}/${Date.now()}`;
  let last: Resource | null = null;
  for (const [i, f] of p.files.entries()) {
    progress({ done: i, total: p.files.length });
    const form = new FormData();
    const rel = relativePath(f).split("/").slice(1).join("/") || f.name;
    const dir = rel.includes("/") ? rel.slice(0, rel.lastIndexOf("/")) : "";
    // The server streams the body, so the fields have to come before the file.
    form.append("scope", "user");
    form.append("scope_id", userId);
    form.append("display_name", f.name);
    form.append("description", `Uploaded to publish to ${site}`);
    form.append("category", "other");
    form.append("path", p.folder && dir ? `${folderPath}/${dir}` : folderPath);
    form.append("file", f, f.name);
    last = await upload(form);
  }
  if (p.folder) return { folder: folderPath, source: p.label };
  return { archive: `mcp:resource:${last!.id}`, source: p.label };
}

function UploadDrop({ picked, onPick, limits }: { picked: Picked | null; onPick: (p: Picked | null) => void; limits?: SiteLimits }) {
  const folderInput = useRef<HTMLInputElement>(null);
  const zipInput = useRef<HTMLInputElement>(null);
  const take = (list: FileList | null, folder: boolean) => {
    const files = list ? Array.from(list) : [];
    if (files.length === 0) return;
    const first = relativePath(files[0]!);
    onPick({ files, folder, label: folder ? first.split("/")[0] || "folder" : files[0]!.name });
  };
  const drop = (e: DragEvent) => {
    e.preventDefault();
    const list = e.dataTransfer.files;
    const single = list.length === 1 && isArchive(list[0]!);
    take(list, !single);
  };
  if (picked) {
    const total = picked.files.reduce((n, f) => n + f.size, 0);
    return (
      <div className="flex items-center justify-between gap-2 rounded border p-3">
        <div className="min-w-0">
          <p className="truncate font-medium">{picked.label}</p>
          <p className="text-xs text-muted-foreground">
            {number.format(picked.files.length)} {picked.files.length === 1 ? "file" : "files"}, {formatBytes(total)}
          </p>
        </div>
        <Button size="sm" variant="outline" onClick={() => onPick(null)}>
          Remove
        </Button>
      </div>
    );
  }
  return (
    <div className="flex flex-col items-center gap-2 rounded border border-dashed p-6 text-center" onDragOver={(e) => e.preventDefault()} onDrop={drop} data-testid="publish-drop">
      <Upload className="h-6 w-6 text-muted-foreground" aria-hidden />
      <p className="text-sm">Drop a folder or a .zip here</p>
      <div className="flex gap-2">
        <Button size="sm" variant="outline" onClick={() => folderInput.current?.click()}>
          Choose folder
        </Button>
        <Button size="sm" variant="outline" onClick={() => zipInput.current?.click()}>
          Choose .zip
        </Button>
      </div>
      <p className="text-xs text-muted-foreground">{limitsText(limits)}</p>
      <input
        ref={(el) => {
          folderInput.current = el;
          el?.setAttribute("webkitdirectory", "");
        }}
        type="file"
        multiple
        hidden
        aria-label="Choose folder"
        onChange={(e) => take(e.target.files, true)}
      />
      <input ref={zipInput} type="file" accept=".zip,.tgz,.tar.gz" hidden aria-label="Choose .zip" onChange={(e) => take(e.target.files, false)} />
    </div>
  );
}

function ResourcePicker({ chosen, onChoose }: { chosen: Resource | null; onChoose: (r: Resource) => void }) {
  const [q, setQ] = useState("");
  const { data, isLoading } = useResources({ q: q.trim() || undefined, limit: 25 });
  const archives = (data?.resources ?? []).filter((r) => /\.(zip|tgz|tar\.gz)$/i.test(r.filename));
  return (
    <div className="space-y-2">
      <div className="space-y-1">
        <Label htmlFor="publish-search">Search Resources</Label>
        <Input id="publish-search" value={q} onChange={(e) => setQ(e.target.value)} />
      </div>
      {isLoading && <p className="text-sm text-muted-foreground">Loading…</p>}
      {!isLoading && archives.length === 0 && <p className="text-sm text-muted-foreground">No archive matches.</p>}
      <p className="text-xs text-muted-foreground">A zip or gzipped tar. A release’s limits are checked before any file is stored.</p>
      <div className="max-h-48 space-y-2 overflow-auto">
        {archives.map((r) => (
          <label key={r.id} className="flex cursor-pointer items-start gap-2 rounded border p-3">
            <input type="radio" name="resource" aria-label={r.filename} checked={chosen?.id === r.id} onChange={() => onChoose(r)} className="mt-1" />
            <span className="min-w-0">
              <span className="block truncate font-medium">/{r.path}/{r.filename}</span>
              <span className="block text-xs text-muted-foreground">Archive, {formatBytes(r.size_bytes)}</span>
            </span>
          </label>
        ))}
      </div>
    </div>
  );
}

export function bar(pct: number) {
  return (
    <div className="h-2 overflow-hidden rounded bg-muted">
      <div className="h-full bg-primary" style={{ width: `${Math.min(100, pct)}%` }} />
    </div>
  );
}
