import { useMemo, useState } from "react";
import { ExternalLink } from "lucide-react";
import {
  DEFAULT_ENVIRONMENT,
  useApprovalForSite,
  usePromoteSite,
  useRollbackSite,
  useSiteCompare,
  useSiteDeployments,
  useSiteReleases,
  type Site,
  type SiteEnvironment,
} from "@/api/portal/hooks/sites";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";
import { useAuthStore } from "@/stores/auth";
import { AddDomainDialog } from "./SiteDomains";
import { AddPersonDialog } from "./SiteAccess";
import { PublishDialog } from "./SitePublish";
import { bareAddress, errorText, hostUrl, hostsOf, orderedEnvironments, when } from "./siteFormat";

type EnvDialog = { kind: "promote" | "rollback"; env: SiteEnvironment } | { kind: "publish" | "domain" | "share" } | null;

// SiteEnvironmentsTab is each environment: the release it serves, who moved it
// and when, and where it answers (#2559). The default environment answers at
// the site's own address to people with access; any other, at the domains added
// to it, to anyone.
export function SiteEnvironmentsTab({ site, onNavigate }: { site: Site; onNavigate: (path: string) => void }) {
  const [dialog, setDialog] = useState<EnvDialog>(null);
  const canEdit = site.permissions.manage;
  const close = () => setDialog(null);
  const envs = orderedEnvironments(site.environments);
  const action = canEdit ? (
    <div className="flex flex-wrap gap-2">
      <Button size="sm" variant="outline" onClick={() => setDialog({ kind: "domain" })}>
        Add domain
      </Button>
      <Button size="sm" onClick={() => setDialog({ kind: "publish" })}>
        Publish
      </Button>
    </div>
  ) : undefined;
  return (
    <>
      <ApprovalBanners site={site} onNavigate={onNavigate} />
      <SectionCard title="Environments" action={action}>
        <div className="overflow-x-auto">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Environment</TableHead>
                <TableHead>Release</TableHead>
                <TableHead>Moved</TableHead>
                <TableHead>Address</TableHead>
                {canEdit && <TableHead />}
              </TableRow>
            </TableHeader>
            <TableBody>
              {envs.map((e) => (
                <EnvironmentRow key={e.name} site={site} env={e} canEdit={canEdit} onAct={setDialog} />
              ))}
            </TableBody>
          </Table>
        </div>
        <p className="mt-3 text-xs text-muted-foreground">
          The site's own address opens only for administrators and the people on the Access tab, after they sign in. A domain is public unless the site asks for a sign-in.
        </p>
      </SectionCard>
      <EnvDialogs site={site} dialog={dialog} onClose={close} onNavigate={onNavigate} />
    </>
  );
}

function EnvDialogs({ site, dialog, onClose, onNavigate }: { site: Site; dialog: EnvDialog; onClose: () => void; onNavigate: (path: string) => void }) {
  switch (dialog?.kind) {
    case "publish":
      return <PublishDialog site={site} onClose={onClose} onNavigate={onNavigate} />;
    case "domain":
      return <AddDomainDialog site={site} onClose={onClose} />;
    case "share":
      return <AddPersonDialog site={site} viewOnly onClose={onClose} />;
    case "promote":
      return <PromoteDialog site={site} target={dialog.env} onClose={onClose} />;
    case "rollback":
      return <RollbackDialog site={site} env={dialog.env} onClose={onClose} />;
    default:
      return null;
  }
}

function EnvironmentRow({
  site,
  env,
  canEdit,
  onAct,
}: {
  site: Site;
  env: SiteEnvironment;
  canEdit: boolean;
  onAct: (d: EnvDialog) => void;
}) {
  const moved = env.release > 0;
  return (
    <TableRow>
      <TableCell>
        <div className="flex flex-wrap items-center gap-2">
          <span className="font-medium">{env.name}</span>
          {env.requires_approval && <Badge variant="warning">Requires approval</Badge>}
        </div>
      </TableCell>
      <TableCell>{moved ? <Badge variant="info">{env.release}</Badge> : <span className="text-muted-foreground">None</span>}</TableCell>
      <TableCell>
        <div>{moved ? when(env.updated_at) : ""}</div>
        <div className="text-xs text-muted-foreground">{moved ? env.updated_by : ""}</div>
      </TableCell>
      <TableCell>
        <EnvironmentAddress site={site} env={env} />
      </TableCell>
      {canEdit && (
        <TableCell>
          <RowActions env={env} onAct={onAct} />
        </TableCell>
      )}
    </TableRow>
  );
}

function EnvironmentAddress({ site, env }: { site: Site; env: SiteEnvironment }) {
  if (env.name === DEFAULT_ENVIRONMENT) {
    return (
      <div className="space-y-0.5">
        {site.address && <AddressLink href={site.address} text={bareAddress(site.address)} />}
        <p className="text-xs text-muted-foreground">Sign-in and access needed</p>
      </div>
    );
  }
  const hosts = hostsOf(site, env.name);
  if (hosts.length === 0) return <span className="text-muted-foreground">No domain</span>;
  return (
    <div className="space-y-0.5">
      {hosts.map((d) => (
        <AddressLink key={d.hostname} href={hostUrl(site, d.hostname)} text={d.hostname} />
      ))}
    </div>
  );
}

function RowActions({ env, onAct }: { env: SiteEnvironment; onAct: (d: EnvDialog) => void }) {
  const own = env.name === DEFAULT_ENVIRONMENT;
  return (
    <div className="flex justify-end gap-2">
      {own ? (
        <Button size="sm" variant="outline" onClick={() => onAct({ kind: "share" })}>
          Share
        </Button>
      ) : (
        <Button size="sm" variant="outline" onClick={() => onAct({ kind: "promote", env })}>
          Promote
        </Button>
      )}
      <Button size="sm" variant="outline" disabled={env.release === 0} onClick={() => onAct({ kind: "rollback", env })}>
        Roll back
      </Button>
    </div>
  );
}

function AddressLink({ href, text }: { href: string; text: string }) {
  return (
    <a className="inline-flex items-center gap-1 break-all font-mono text-sm text-primary hover:underline" href={href} target="_blank" rel="noreferrer" title="Open in a new tab">
      {text}
      <ExternalLink className="h-3 w-3 shrink-0" aria-hidden />
    </a>
  );
}

// ApprovalBanners says where a promote that needs approval stands.
function ApprovalBanners({ site, onNavigate }: { site: Site; onNavigate: (path: string) => void }) {
  const requests = useApprovalForSite(site);
  const promote = usePromoteSite(site.name);
  const email = useAuthStore((s) => s.user?.email ?? "");
  const [failure, setFailure] = useState<string | null>(null);
  return (
    <>
      {requests.map((r) => {
        const finish = () =>
          promote.mutate({ to: r.target, release: r.version }, { onError: (e) => setFailure(errorText(e, "The promote could not be finished")) });
        const mine = r.requester_email === email || site.permissions.manage;
        if (r.status === "pending") {
          return (
            <Alert key={r.id} data-testid="approval-waiting">
              <AlertTitle>
                Release {r.version} is waiting for approval to serve {r.target}
              </AlertTitle>
              <AlertDescription className="flex flex-wrap items-center justify-between gap-2">
                <span>
                  Asked by {r.requester_email} on {when(r.created_at)}. {r.target} keeps serving its release until this is approved and promoted.
                </span>
                <Button size="sm" variant="outline" onClick={() => onNavigate("/approvals")}>
                  Open in Approvals
                </Button>
              </AlertDescription>
            </Alert>
          );
        }
        if (r.status === "approved" && mine) {
          return (
            <Alert key={r.id} data-testid="approval-approved">
              <AlertTitle>
                Release {r.version} is approved for {r.target}
              </AlertTitle>
              <AlertDescription className="flex flex-wrap items-center justify-between gap-2">
                <span>
                  Approved by {r.decided_by} on {when(r.decided_at ?? "")}. Nothing changes on {r.target} until you finish the promote.
                </span>
                <Button size="sm" onClick={finish} disabled={promote.isPending}>
                  Finish promote
                </Button>
              </AlertDescription>
            </Alert>
          );
        }
        return (
          <Alert key={r.id} variant="destructive" data-testid="approval-rejected">
            <AlertTitle>
              Release {r.version} was rejected for {r.target}
            </AlertTitle>
            <AlertDescription>
              Rejected by {r.decided_by} on {when(r.decided_at ?? "")}
              {r.decision_comment ? `: "${r.decision_comment}"` : "."}
            </AlertDescription>
          </Alert>
        );
      })}
      {failure && (
        <Alert variant="destructive">
          <AlertDescription>{failure}</AlertDescription>
        </Alert>
      )}
    </>
  );
}

function PromoteDialog({ site, target, onClose }: { site: Site; target: SiteEnvironment; onClose: () => void }) {
  const isAdmin = useAuthStore((s) => s.isAdmin());
  const promote = usePromoteSite(site.name);
  const sources = useMemo(() => orderedEnvironments(site.environments).filter((e) => e.name !== target.name && e.release > 0), [site, target]);
  const [from, setFrom] = useState(sources[0]?.name ?? "");
  const [reason, setReason] = useState("");
  const [failure, setFailure] = useState<string | null>(null);
  const source = sources.find((s) => s.name === from);
  const asks = target.requires_approval && !isAdmin;
  const cmp = useSiteCompare(site.name, target.release, source?.release ?? 0);
  const submit = () => {
    setFailure(null);
    promote.mutate(
      { from, to: target.name, reason: reason.trim() },
      { onSuccess: onClose, onError: (e) => setFailure(errorText(e, "The promote could not be done")) },
    );
  };
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>Promote to {target.name}</DialogTitle>
        </DialogHeader>
        <div className="space-y-3">
          <div className="space-y-1">
            <Label>Release from</Label>
            <Select value={from} onValueChange={setFrom}>
              <SelectTrigger aria-label="Release from">
                <SelectValue placeholder="No environment serves a release yet" />
              </SelectTrigger>
              <SelectContent>
                {sources.map((s) => (
                  <SelectItem key={s.name} value={s.name}>
                    {s.name}, release {s.release}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          {source && <Changes target={target} data={cmp.data} loading={cmp.isLoading} />}
          <PromoteReason asks={asks} value={reason} onChange={setReason} />
          <ApprovalNote target={target} asks={asks} />
          <Failure text={failure} />
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={!source || promote.isPending}>
            {asks ? "Ask for approval" : `Promote to ${target.name}`}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function Failure({ text }: { text: string | null }) {
  if (!text) return null;
  return (
    <Alert variant="destructive">
      <AlertDescription>{text}</AlertDescription>
    </Alert>
  );
}

function PromoteReason({ asks, value, onChange }: { asks: boolean; value: string; onChange: (v: string) => void }) {
  if (!asks) return null;
  return (
    <div className="space-y-1">
      <Label htmlFor="promote-reason">Why this release</Label>
      <Textarea id="promote-reason" value={value} onChange={(e) => onChange(e.target.value)} />
    </div>
  );
}

function ApprovalNote({ target, asks }: { target: SiteEnvironment; asks: boolean }) {
  if (!target.requires_approval) return null;
  return (
    <p className="text-xs text-muted-foreground">
      {asks
        ? `${target.name} requires approval. This sends a request to Approvals, and ${target.name} keeps serving its release until it is approved and you finish the promote.`
        : `${target.name} requires approval. Because you are an administrator, this records your approval and serves the release at once.`}
    </p>
  );
}

function Changes({
  target,
  data,
  loading,
}: {
  target: SiteEnvironment;
  data?: { changed: string[]; added: string[]; removed: string[]; redirects_added: unknown[] };
  loading: boolean;
}) {
  if (loading || !data) return <p className="text-sm text-muted-foreground">Comparing…</p>;
  const rows = [
    ...data.changed.map((p) => ({ p, label: "Changed", variant: "info" as const })),
    ...data.added.map((p) => ({ p, label: "Added", variant: "success" as const })),
    ...data.removed.map((p) => ({ p, label: "Removed", variant: "danger" as const })),
  ];
  return (
    <div className="space-y-2">
      <p className="text-sm font-medium">What changes on {target.name}</p>
      <div className="flex flex-wrap gap-2">
        <Badge variant="info">{data.changed.length} files changed</Badge>
        <Badge variant="success">{data.added.length} added</Badge>
        <Badge variant="danger">{data.removed.length} removed</Badge>
        <Badge variant="muted">{data.redirects_added.length} redirects added</Badge>
      </div>
      {rows.length > 0 && (
        <div className="max-h-56 overflow-auto rounded border">
          <Table>
            <TableBody>
              {rows.map((r) => (
                <TableRow key={r.label + r.p}>
                  <TableCell className="w-24">
                    <Badge variant={r.variant}>{r.label}</Badge>
                  </TableCell>
                  <TableCell className="break-all font-mono text-sm">{r.p}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
    </div>
  );
}

function RollbackDialog({ site, env, onClose }: { site: Site; env: SiteEnvironment; onClose: () => void }) {
  const rollback = useRollbackSite(site.name);
  const deployments = useSiteDeployments(site.name, env.name);
  const releases = useSiteReleases(site.name);
  const options = useMemo(() => {
    const seen = new Set<number>();
    for (const d of deployments.data?.deployments ?? []) if (d.to !== env.release) seen.add(d.to);
    return [...seen].sort((a, b) => b - a);
  }, [deployments.data, env.release]);
  const [picked, setPicked] = useState<number | null>(null);
  const chosen = picked ?? options[0] ?? 0;
  const [failure, setFailure] = useState<string | null>(null);
  const note = (n: number) => releases.data?.releases.find((r) => r.number === n)?.note ?? "";
  const submit = () => {
    setFailure(null);
    rollback.mutate(
      { environment: env.name, release: chosen },
      { onSuccess: onClose, onError: (e) => setFailure(errorText(e, "The roll back could not be done")) },
    );
  };
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Roll back {env.name}</DialogTitle>
          <DialogDescription>
            {env.name} is serving release {env.release}. Choose a release it served before. It takes effect at once, and no approval is needed.
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-2">
          {options.length === 0 && <p className="text-sm text-muted-foreground">{env.name} has served no earlier release.</p>}
          {options.map((n) => (
            <label key={n} className="flex cursor-pointer items-start gap-2 rounded border p-3">
              <input type="radio" name="rollback" checked={chosen === n} onChange={() => setPicked(n)} className="mt-1" />
              <span>
                <span className="block font-medium">Release {n}</span>
                <span className="block text-xs text-muted-foreground">{note(n)}</span>
              </span>
            </label>
          ))}
          {failure && (
            <Alert variant="destructive">
              <AlertDescription>{failure}</AlertDescription>
            </Alert>
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={chosen === 0 || rollback.isPending}>
            Roll back to release {chosen}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
