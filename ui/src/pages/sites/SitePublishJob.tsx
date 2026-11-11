import { Square } from "lucide-react";
import {
  DEV_ENVIRONMENT,
  useSiteActiveJobs,
  useSiteJob,
  useSiteRelease,
  useStopSiteJob,
  type Site,
  type SiteJob,
} from "@/api/portal/hooks/sites";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { envOpenUrl, formatBytes, when } from "./siteFormat";

// A publish running as a job (#2593): its four phases while it runs, a banner
// on the Environments tab once its dialog is closed (with Stop, for anyone who
// manages the site), and how it ended: published, refused with the server's
// own sentence, or stopped. dev keeps serving its release until the last step.

const count = new Intl.NumberFormat("en-US");

/** The phases a publish moves through, in order, and how the server names each. */
const PHASES: { match: RegExp; name: (next: number) => string }[] = [
  { match: /^Reading/, name: () => "Reading files" },
  { match: /^Writing/, name: () => "Writing files" },
  { match: /^Rendering/, name: () => "Rendering pages" },
  { match: /^Switching/, name: (next) => `Switching dev to release ${next}` },
];

/** phaseIndex is which phase the job's phase sentence is, 0 before it says. */
export function phaseIndex(phase: string): number {
  const i = PHASES.findIndex((p) => p.match.test(phase));
  return i < 0 ? 0 : i;
}

function devRelease(site: Site): number {
  return site.environments.find((e) => e.name === DEV_ENVIRONMENT)?.release ?? 0;
}

/** progressLine is the running phase's count, with bytes once there are any. */
function progressLine(job: SiteJob): string {
  if (job.total <= 0) return "Working";
  const files = `${count.format(job.done)} of ${count.format(job.total)}`;
  return job.bytes > 0 ? `${files} · ${formatBytes(job.bytes)}` : files;
}

function Bar({ pct }: { pct: number }) {
  return (
    <div className="h-1.5 overflow-hidden rounded bg-muted">
      <div className="h-full bg-primary" style={{ width: `${Math.max(0, Math.min(100, pct))}%` }} />
    </div>
  );
}

function Phases({ job, next }: { job: SiteJob; next: number }) {
  const at = phaseIndex(job.phase);
  return (
    <ol className="space-y-3" data-testid="publish-phases">
      {PHASES.map((p, i) => {
        let right = "Waiting";
        let pct = 0;
        if (i < at) [right, pct] = ["Done", 100];
        else if (i === at) [right, pct] = [progressLine(job), job.total > 0 ? (job.done / job.total) * 100 : 5];
        return (
          <li key={i} className="space-y-1" data-testid={`publish-phase-${i}`}>
            <div className="flex flex-wrap justify-between gap-2 text-sm">
              <span className={i === at ? "font-medium" : i < at ? "" : "text-muted-foreground"}>{p.name(next)}</span>
              <span className="text-muted-foreground">{right}</span>
            </div>
            <Bar pct={pct} />
          </li>
        );
      })}
    </ol>
  );
}

/** JobDialog follows one publish job by id and shows how it stands or ended. */
export function JobDialog({
  site,
  jobId,
  onClose,
  onAgain,
  onNavigate,
}: {
  site: Site;
  jobId: string;
  onClose: () => void;
  onAgain?: () => void;
  onNavigate: (path: string) => void;
}) {
  const job = useSiteJob(site.name, jobId).data;
  switch (job?.status) {
    case "done":
      return <PublishDone site={site} job={job} onClose={onClose} onNavigate={onNavigate} />;
    case "failed":
      return <PublishRefused site={site} message={job.error ?? ""} onClose={onClose} onAgain={onAgain} />;
    case "stopped":
      return <PublishStopped site={site} onClose={onClose} onAgain={onAgain} />;
    default:
      return <PublishProgress site={site} job={job} onClose={onClose} />;
  }
}

function PublishProgress({ site, job, onClose }: { site: Site; job?: SiteJob; onClose: () => void }) {
  const stop = useStopSiteJob(site.name);
  const now = devRelease(site);
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-h-[90dvh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>Publishing to dev</DialogTitle>
          <DialogDescription className="break-all">
            {site.name}
            {job ? ` · from ${job.source} · started ${when(job.created_at)} by ${job.created_by}` : ""}
          </DialogDescription>
        </DialogHeader>
        {job ? <Phases job={job} next={now + 1} /> : <p className="text-sm text-muted-foreground">Starting…</p>}
        <p className="text-xs text-muted-foreground">dev keeps serving release {now} until the last step. Closing this window doesn’t stop it.</p>
        <DialogFooter>
          {job && (
            <Button variant="outline" onClick={() => stop.mutate(job.id)} disabled={stop.isPending}>
              <Square className="h-3.5 w-3.5" aria-hidden />
              Stop
            </Button>
          )}
          <Button onClick={onClose}>Close</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function PublishDone({ site, job, onClose, onNavigate }: { site: Site; job: SiteJob; onClose: () => void; onNavigate: (path: string) => void }) {
  const url = envOpenUrl(site, job.environment || DEV_ENVIRONMENT);
  const release = useSiteRelease(site.name, job.release).data;
  const files = release?.file_count ?? job.total;
  const others = site.environments.filter((e) => e.name !== DEV_ENVIRONMENT).map((e) => e.name);
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Published to dev</DialogTitle>
        </DialogHeader>
        <Alert variant="success" data-testid="publish-done">
          <AlertTitle>dev serves release {job.release}</AlertTitle>
          <AlertDescription>
            {count.format(files)} files{release ? `, ${formatBytes(release.total_bytes)}` : ""}, from {job.source}.{job.pages > 0 ? ` ${count.format(job.pages)} pages rendered.` : ""}
          </AlertDescription>
        </Alert>
        <p className="text-sm text-muted-foreground">
          {others.length > 0 ? `${others.join(" and ")} ${others.length === 1 ? "is" : "are"} unchanged. ` : ""}Promote release {job.release} when it is ready.
        </p>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Close
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

/** PublishRefused shows the server's own sentence: what was not done, and the file, limit or connection. */
export function PublishRefused({ site, message, onClose, onAgain }: { site: Site; message: string; onClose: () => void; onAgain?: () => void }) {
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Nothing was published</DialogTitle>
        </DialogHeader>
        <Alert variant="destructive" data-testid="publish-refused">
          <AlertDescription className="break-words">{message || "The publish could not be finished; try again."}</AlertDescription>
        </Alert>
        <p className="text-sm text-muted-foreground">
          dev still serves release {devRelease(site)}. Files a system leaves behind, such as .DS_Store, are dropped without asking.
        </p>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Close
          </Button>
          {onAgain && <Button onClick={onAgain}>Choose another source</Button>}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function PublishStopped({ site, onClose, onAgain }: { site: Site; onClose: () => void; onAgain?: () => void }) {
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Publish stopped</DialogTitle>
        </DialogHeader>
        <Alert variant="warning" data-testid="publish-stopped">
          <AlertTitle>The publish was stopped</AlertTitle>
          <AlertDescription>Nothing was published, and dev still serves release {devRelease(site)}. Files already written are cleaned up later.</AlertDescription>
        </Alert>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Close
          </Button>
          {onAgain && <Button onClick={onAgain}>Publish again</Button>}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/** PublishBanner is each publish still running, on the Environments tab, with Stop. */
export function PublishBanner({ site, onShow }: { site: Site; onShow: (jobId: string) => void }) {
  const jobs = (useSiteActiveJobs(site.name).data?.jobs ?? []).filter((j) => j.kind === "publish" && j.status === "running");
  const stop = useStopSiteJob(site.name);
  if (jobs.length === 0 || !site.permissions.manage) return null;
  const now = devRelease(site);
  return (
    <>
      {jobs.map((j) => (
        <Alert key={j.id} data-testid="publish-banner">
          <AlertTitle className="break-all">Publishing to dev from {j.source}</AlertTitle>
          <AlertDescription className="flex flex-wrap items-center justify-between gap-2">
            <span>
              {PHASES[phaseIndex(j.phase)]!.name(now + 1)}: {progressLine(j)}. dev keeps serving release {now} until it finishes.
            </span>
            <span className="flex gap-2">
              <Button size="sm" variant="outline" onClick={() => onShow(j.id)}>
                Show progress
              </Button>
              <Button size="sm" variant="outline" onClick={() => stop.mutate(j.id)} disabled={stop.isPending}>
                <Square className="h-3.5 w-3.5" aria-hidden />
                Stop
              </Button>
            </span>
          </AlertDescription>
        </Alert>
      ))}
    </>
  );
}
