import { useState } from "react";
import { DEFAULT_ENVIRONMENT, useCopySite, useSiteForms, useSiteJob, useSites, useStopSiteJob, type Site, type SiteJob, type SiteLimits } from "@/api/portal/hooks/sites";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { EnvironmentSelect, bar, number } from "./SitePublish";
import { bareAddress, errorText, formatBytes } from "./siteFormat";

// CopySiteDialog copies a public website into a new release, in the background,
// and reports what it could not bring across and why.
export function CopySiteDialog({ site, onClose }: { site: Site; onClose: () => void }) {
  const limits = useSites().data?.limits;
  const copy = useCopySite(site.name);
  const [url, setUrl] = useState("");
  const [env, setEnv] = useState(DEFAULT_ENVIRONMENT);
  const [note, setNote] = useState("");
  const [forms, setForms] = useState<FormRow[]>([]);
  const [jobId, setJobId] = useState<string | null>(null);
  const [failure, setFailure] = useState<string | null>(null);
  const job = useSiteJob(site.name, jobId).data;
  const host = hostOf(url);
  const start = () => {
    setFailure(null);
    copy.mutate(
      { url: url.trim(), environment: env, note: note.trim(), form_map: formMap(forms) },
      { onSuccess: (j) => setJobId(j.id), onError: (e) => setFailure(errorText(e, "The copy could not be started")) },
    );
  };
  if (jobId) return <CopyJob site={site} host={host} jobId={jobId} job={job} limits={limits} onClose={onClose} />;
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>Copy a site</DialogTitle>
          <DialogDescription>Copies a public website into a new release. Its pages keep their addresses, and its redirects become this site's redirect map.</DialogDescription>
        </DialogHeader>
        <div className="space-y-3">
          <div className="space-y-1">
            <Label htmlFor="copy-url">Website address</Label>
            <Input id="copy-url" placeholder="https://www.example.com" value={url} onChange={(e) => setUrl(e.target.value)} />
          </div>
          <div className="grid gap-3 sm:grid-cols-2">
            <div className="space-y-1">
              <EnvironmentSelect site={site} value={env} onChange={setEnv} />
            </div>
            <div className="space-y-1">
              <Label htmlFor="copy-note">Note</Label>
              <Input id="copy-note" value={note} onChange={(e) => setNote(e.target.value)} />
            </div>
          </div>
          <FormRows site={site.name} rows={forms} onChange={setForms} />
          <CopyBounds limits={limits} />
          <CopyFailure text={failure} />
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={start} disabled={!host || copy.isPending}>
            Copy site
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

interface FormRow {
  page: string;
  form: string;
}

function formMap(rows: FormRow[]): Record<string, string> {
  return Object.fromEntries(rows.filter((r) => r.page.trim() && r.form).map((r) => [r.page.trim(), r.form]));
}

// FormRows says which platform form replaces a copied page's forms, since a
// copied form still posts to the old site.
function FormRows({ site, rows, onChange }: { site: string; rows: FormRow[]; onChange: (r: FormRow[]) => void }) {
  const forms = useSiteForms(site).data?.forms ?? [];
  const update = (i: number, patch: Partial<FormRow>) => onChange(rows.map((r, n) => (n === i ? { ...r, ...patch } : r)));
  return (
    <div className="space-y-2">
      <Label>Forms</Label>
      <p className="text-xs text-muted-foreground">A copied form still posts to the old site. Name a platform form to post to it instead.</p>
      {rows.map((r, i) => (
        <div key={i} className="flex items-center gap-2">
          <Input aria-label="Page path" placeholder="/contact" value={r.page} onChange={(e) => update(i, { page: e.target.value })} />
          <Select value={r.form} onValueChange={(form) => update(i, { form })}>
            <SelectTrigger aria-label="Platform form">
              <SelectValue placeholder="Platform form" />
            </SelectTrigger>
            <SelectContent>
              {forms.map((f) => (
                <SelectItem key={f} value={f}>
                  {f}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <Button variant="ghost" aria-label="Remove this form" onClick={() => onChange(rows.filter((_, n) => n !== i))}>
            Remove
          </Button>
        </div>
      ))}
      <Button size="sm" variant="outline" disabled={forms.length === 0} onClick={() => onChange([...rows, { page: "", form: "" }])}>
        Add form
      </Button>
    </div>
  );
}

function CopyJob({ site, host, jobId, job, limits, onClose }: { site: Site; host: string; jobId: string; job?: SiteJob; limits?: SiteLimits; onClose: () => void }) {
  if (job && job.status !== "running") return <CopyResult site={site} job={job} onClose={onClose} />;
  return <Copying site={site.name} host={host} jobId={jobId} job={job} max={limits?.crawl_max_pages ?? 0} maxBytes={limits?.crawl_max_bytes ?? 0} onClose={onClose} />;
}

function CopyFailure({ text }: { text: string | null }) {
  if (!text) return null;
  return (
    <Alert variant="destructive">
      <AlertDescription>{text}</AlertDescription>
    </Alert>
  );
}

function CopyBounds({ limits }: { limits?: SiteLimits }) {
  if (!limits) return null;
  return (
    <p className="text-xs text-muted-foreground">
      Copies at most {number.format(limits.crawl_max_pages)} pages and {formatBytes(limits.crawl_max_bytes)}.
    </p>
  );
}

function CopyFacts({ job, maxBytes }: { job?: SiteJob; maxBytes: number }) {
  const facts: [string, string][] = [
    ["Downloaded", `${formatBytes(job?.bytes ?? 0)} of at most ${formatBytes(maxBytes)}`],
    ["Redirects found", number.format(job?.redirects ?? 0)],
    ["Could not copy", number.format(job?.skipped_count ?? 0)],
  ];
  return (
    <dl className="grid grid-cols-3 gap-3">
      {facts.map(([k, v]) => (
        <div key={k}>
          <dt className="text-xs text-muted-foreground">{k}</dt>
          <dd className="text-sm">{v}</dd>
        </div>
      ))}
    </dl>
  );
}

function Copying({ site, host, jobId, job, max, maxBytes, onClose }: { site: string; host: string; jobId: string; job?: SiteJob; max: number; maxBytes: number; onClose: () => void }) {
  const stop = useStopSiteJob(site);
  const pages = job?.pages ?? 0;
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Copying {host}</DialogTitle>
        </DialogHeader>
        <div className="space-y-3">
          <div>
            <div className="flex justify-between text-sm">
              <span className="font-medium">{number.format(pages)} pages copied</span>
              <span className="text-muted-foreground">At most {number.format(max)}</span>
            </div>
            {bar(max ? (pages / max) * 100 : 0)}
          </div>
          <CopyFacts job={job} maxBytes={maxBytes} />
          <p className="text-xs text-muted-foreground">You can close this and the copy continues. It appears under Releases when it finishes.</p>
        </div>
        <DialogFooter>
          <Button variant="ghost" disabled={stop.isPending} onClick={() => stop.mutate(jobId)}>
            Stop copy
          </Button>
          <Button variant="outline" onClick={onClose}>
            Close
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function hostOf(url: string): string {
  try {
    return new URL(url.trim()).host;
  } catch {
    return "";
  }
}

function CopyResult({ site, job, onClose }: { site: Site; job: SiteJob; onClose: () => void }) {
  const title =
    job.status === "done" ? `Copied into release ${job.release}` : job.status === "stopped" ? "The copy was stopped" : "The copy could not be finished";
  const address = job.environment === DEFAULT_ENVIRONMENT && site.address ? site.address : "";
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
        </DialogHeader>
        <div className="space-y-3">
          {job.error && (
            <Alert variant="destructive">
              <AlertDescription>{job.error}</AlertDescription>
            </Alert>
          )}
          <dl className="grid grid-cols-3 gap-3">
            <div>
              <dt className="text-xs text-muted-foreground">Pages</dt>
              <dd>{number.format(job.pages)}</dd>
            </div>
            <div>
              <dt className="text-xs text-muted-foreground">Redirects</dt>
              <dd>{number.format(job.redirects)}</dd>
            </div>
            <div>
              <dt className="text-xs text-muted-foreground">Serving</dt>
              <dd>{job.environment}</dd>
            </div>
          </dl>
          {job.skipped.length > 0 && (
            <>
              <Alert>
                <AlertTitle>{job.skipped.length} addresses could not be copied</AlertTitle>
                <AlertDescription>Check that the site does not need them.</AlertDescription>
              </Alert>
              <div className="max-h-56 overflow-auto rounded border">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Address</TableHead>
                      <TableHead>Reason</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {job.skipped.map((s) => (
                      <TableRow key={s.address}>
                        <TableCell className="break-all font-mono text-sm">{s.address}</TableCell>
                        <TableCell>{s.reason.charAt(0).toUpperCase() + s.reason.slice(1)}</TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
            </>
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Done
          </Button>
          {address && job.status === "done" && (
            <Button asChild>
              <a href={address} target="_blank" rel="noreferrer" title={bareAddress(address)}>
                Open site
              </a>
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
