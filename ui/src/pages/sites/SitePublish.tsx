import { useRef, useState, type DragEvent } from "react";
import { Upload } from "lucide-react";
import {
  DEFAULT_ENVIRONMENT,
  usePublishSite,
  useSiteJob,
  useSiteRelease,
  useSites,
  type Site,
  type SiteJob,
  type SiteLimits,
} from "@/api/portal/hooks/sites";
import { useResources, useUploadResource } from "@/api/resources/hooks";
import type { Resource } from "@/api/resources/types";
import { useAuthStore } from "@/stores/auth";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { errorText, formatBytes, hostUrl, orderedEnvironments } from "./siteFormat";

export const number = new Intl.NumberFormat("en-US");

function publishable(site: Site) {
  return orderedEnvironments(site.environments).filter((e) => !e.requires_approval);
}

function limitsText(l?: SiteLimits): string {
  if (!l) return "";
  return `Up to ${number.format(l.max_release_files)} files, ${formatBytes(l.max_file_bytes)} per file, ${formatBytes(l.max_release_bytes)} in all. A _redirects file becomes the redirect map and 404.html the not-found page.`;
}

interface Picked {
  label: string;
  files: File[];
  folder: boolean;
}

// PublishDialog uploads a folder or a zip through the managed-resource upload,
// or picks one already there, and publishes from it, so there is one publish
// path (#2559). The work runs on the server: closing the dialog does not stop it.
export function PublishDialog({ site, onClose, onNavigate }: { site: Site; onClose: () => void; onNavigate: (path: string) => void }) {
  const limits = useSites().data?.limits;
  const [form, setForm] = useState<PublishForm>({ source: "upload", picked: null, resource: null, env: DEFAULT_ENVIRONMENT, note: "" });
  const flow = usePublishFlow(site.name, form);
  const job = flow.job;
  if (flow.jobId && job?.status === "done") return <Published site={site} job={job} onClose={onClose} onNavigate={onNavigate} />;
  if (flow.jobId && job?.status !== "failed") return <Publishing env={form.env} file={pickedLabel(form)} job={job} onClose={onClose} />;
  const { refusal, ready } = publishState(form, limits, job, flow.failure);
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>Publish to {site.name}</DialogTitle>
        </DialogHeader>
        <PublishFields site={site} form={form} onChange={setForm} limits={limits} refusal={refusal} uploading={flow.uploading} />
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={() => void flow.start()} disabled={!ready || flow.busy}>
            Publish
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// publishState is what the form says now: why it cannot go ahead, if so.
function publishState(form: PublishForm, limits: SiteLimits | undefined, job: SiteJob | undefined, failure: string | null) {
  const tooBig = limits && form.picked ? oversize(form.picked.files, limits) : null;
  const refusal = job?.status === "failed" ? job.error : (tooBig ?? failure);
  const ready = form.source === "upload" ? !!form.picked && !tooBig : !!form.resource;
  return { refusal, ready };
}

function pickedLabel(form: PublishForm): string {
  return form.picked?.label ?? form.resource?.filename ?? "";
}

interface PublishForm {
  source: "upload" | "resources";
  picked: Picked | null;
  resource: Resource | null;
  env: string;
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
  const job = useSiteJob(site, jobId).data;
  const start = async () => {
    setFailure(null);
    try {
      const target = form.source === "resources" ? resourceTarget(form.resource!) : await uploadPicked(form.picked!, { site, userId }, upload.mutateAsync, setUploading);
      const j = await publish.mutateAsync({ environment: form.env, note: form.note.trim(), ...target });
      setJobId(j.id);
    } catch (e) {
      setFailure(errorText(e, "The publish could not be started"));
    } finally {
      setUploading(null);
    }
  };
  return { start, jobId, job, failure, uploading, busy: !!uploading || publish.isPending };
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
  const held = site.environments.filter((e) => e.requires_approval).map((e) => e.name);
  return (
    <div className="space-y-3">
      {refusal && (
        <Alert variant="destructive" data-testid="publish-refused">
          <AlertTitle>Nothing was published</AlertTitle>
          <AlertDescription>{refusal}</AlertDescription>
        </Alert>
      )}
      <Tabs value={form.source} onValueChange={(v) => set({ source: v as PublishForm["source"] })}>
        <TabsList>
          <TabsTrigger value="upload">Upload</TabsTrigger>
          <TabsTrigger value="resources">From Resources</TabsTrigger>
        </TabsList>
      </Tabs>
      {form.source === "upload" ? (
        <UploadDrop picked={form.picked} onPick={(picked) => set({ picked })} limits={limits} />
      ) : (
        <ResourcePicker chosen={form.resource} onChoose={(resource) => set({ resource })} />
      )}
      {uploading && (
        <p className="text-sm text-muted-foreground">
          Uploading {uploading.done} of {uploading.total}
        </p>
      )}
      <div className="grid gap-3 sm:grid-cols-2">
        <div className="space-y-1">
          <EnvironmentSelect site={site} value={form.env} onChange={(env) => set({ env })} />
          {held.length > 0 && <p className="text-xs text-muted-foreground">{held.join(", ")} requires approval, so it is reached by Promote.</p>}
        </div>
        <div className="space-y-1">
          <Label htmlFor="publish-note">Note</Label>
          <Input id="publish-note" placeholder="What changed" value={form.note} onChange={(e) => set({ note: e.target.value })} />
        </div>
      </div>
    </div>
  );
}

export function EnvironmentSelect({ site, value, onChange }: { site: Site; value: string; onChange: (v: string) => void }) {
  return (
    <>
      <Label>Environment</Label>
      <Select value={value} onValueChange={onChange}>
        <SelectTrigger aria-label="Environment">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {publishable(site).map((e) => (
            <SelectItem key={e.name} value={e.name}>
              {e.name}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </>
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

function Publishing({ env, file, job, onClose }: { env: string; file: string; job?: SiteJob; onClose: () => void }) {
  const total = job?.total ?? 0;
  const done = job?.done ?? 0;
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Publishing to {env}</DialogTitle>
        </DialogHeader>
        <div className="space-y-3">
          <div>
            <div className="flex justify-between text-sm">
              <span className="font-medium">{job?.phase || `Uploading ${file}`}</span>
              <span className="text-muted-foreground">{total > 0 ? `${number.format(done)} of ${number.format(total)}` : ""}</span>
            </div>
            {bar(total > 0 ? (done / total) * 100 : 0)}
          </div>
          <p className="text-xs text-muted-foreground">{env} keeps serving its release until every file is written. You can close this and the publish continues.</p>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Close
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function Published({ site, job, onClose, onNavigate }: { site: Site; job: SiteJob; onClose: () => void; onNavigate: (path: string) => void }) {
  const url = publishedUrl(site, job);
  const release = useSiteRelease(site.name, job.release).data;
  const facts: [string, string][] = [
    ["Files", number.format(release?.file_count ?? job.total)],
    ["Size", release ? formatBytes(release.total_bytes) : ""],
    ["Redirects", number.format(release?.redirects.length ?? 0)],
  ];
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            Release {job.release} is serving {job.environment}
          </DialogTitle>
        </DialogHeader>
        <dl className="grid grid-cols-3 gap-3">
          {facts.map(([k, v]) => (
            <div key={k}>
              <dt className="text-xs text-muted-foreground">{k}</dt>
              <dd>{v}</dd>
            </div>
          ))}
        </dl>
        {url && <p className="break-all rounded border p-2 font-mono text-sm">{url}</p>}
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Done
          </Button>
          {url && (
            <Button variant="outline" asChild>
              <a href={url} target="_blank" rel="noreferrer">
                Open site
              </a>
            </Button>
          )}
          <Button onClick={() => onNavigate(`/sites/${encodeURIComponent(site.name)}/environments`)}>Promote</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function publishedUrl(site: Site, job: SiteJob): string {
  if (job.environment === DEFAULT_ENVIRONMENT) return site.address;
  const host = site.domains.find((d) => d.environment === job.environment)?.hostname;
  return host ? hostUrl(site, host) : "";
}
