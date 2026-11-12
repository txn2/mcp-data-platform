import { useMemo, useState } from "react";
import {
  answersAnyone,
  previousEnvironment,
  usePromoteSite,
  useRollbackSite,
  useSiteAccess,
  useSiteCompare,
  useSiteDeployments,
  useSiteReleases,
  type Site,
  type SiteEnvironment,
} from "@/api/portal/hooks/sites";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Label } from "@/components/ui/label";
import { Table, TableBody, TableCell, TableRow } from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";
import { useAuthStore } from "@/stores/auth";
import { EVERYONE_INTERNAL, Failure, grantName, grantOpens } from "./SiteParts";
import { bareAddress, envAddress, errorText } from "./siteFormat";
import { rollbackChoices, rollbackNote } from "./SiteRetention";

// Promote serves the release of the environment immediately before the target
// (#2617), one step at a time; Roll back returns an environment to a release it
// served before (#2559). Both move a pointer and nothing else (#2604).

export function PromoteDialog({ site, target, onClose }: { site: Site; target: SiteEnvironment; onClose: () => void }) {
  const isAdmin = useAuthStore((s) => s.isAdmin());
  const promote = usePromoteSite(site.name);
  const prev = previousEnvironment(site, target.name);
  const source = prev && prev.release > 0 ? prev : undefined;
  const [reason, setReason] = useState("");
  const [failure, setFailure] = useState<string | null>(null);
  const asks = target.requires_approval && !isAdmin;
  const cmp = useSiteCompare(site.name, target.release, source?.release ?? 0);
  const submit = () => {
    if (!source) return;
    setFailure(null);
    promote.mutate(
      { from: source.name, to: target.name, release: source.release, reason: reason.trim() },
      { onSuccess: onClose, onError: (e) => setFailure(errorText(e, "The promote could not be done")) },
    );
  };
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>Promote to {target.name}</DialogTitle>
          <DialogDescription>{site.name}</DialogDescription>
        </DialogHeader>
        <div className="space-y-3">
          <PromoteSource target={target} prev={prev} />
          {source && (
            <>
              <Changes target={target} data={cmp.data} loading={cmp.isLoading} />
              <WhoSees site={site} target={target} release={source.release} />
            </>
          )}
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

// PromoteSource names the one place a promotion takes its release from: the
// environment before the target, and the release it serves.
function PromoteSource({ target, prev }: { target: SiteEnvironment; prev?: SiteEnvironment }) {
  if (!prev) return null;
  if (prev.release === 0) {
    return (
      <p className="rounded border border-dashed p-3 text-sm" data-testid="promote-nothing">
        {prev.name} serves no release yet, and {target.name} takes releases only from {prev.name}. Promote to {prev.name} first.
      </p>
    );
  }
  return (
    <div className="space-y-1">
      <p className="text-sm font-medium">Release from</p>
      <p className="text-sm" data-testid="promote-source">
        {prev.name}, release {prev.release}
      </p>
      <p className="text-xs text-muted-foreground">
        {target.name} takes releases only from {prev.name}, the environment before it.
      </p>
    </div>
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

// WhoSees names who will open the release once it is promoted: the reason to
// stop before promoting to the wrong place.
function WhoSees({ site, target, release }: { site: Site; target: SiteEnvironment; release: number }) {
  const access = useSiteAccess(site.name, site.permissions.manage).data;
  const at = envAddress(site, target.name);
  const where = at ? <span className="font-mono">{bareAddress(at)}</span> : target.name;
  const named = (access?.grants ?? []).filter((g) => grantOpens(g, target.name)).map(grantName);
  if (target.internal_view) named.unshift(EVERYONE_INTERNAL.toLowerCase());
  const who = [...named, "the people who manage this site", "administrators"].join(", ");
  return (
    <div className="space-y-1 rounded border p-3">
      <p className="text-sm font-medium">Who sees it</p>
      {answersAnyone(target) && (
        <p className="text-sm">
          Anyone at <span className="font-mono">{target.public_domains.join(", ")}</span> sees release {release}, with no sign-in.
        </p>
      )}
      <p className="text-sm">
        After the promote, {where} serves release {release} to {who}, after a sign-in.
      </p>
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

export function RollbackDialog({ site, env, onClose }: { site: Site; env: SiteEnvironment; onClose: () => void }) {
  const rollback = useRollbackSite(site.name);
  const deployments = useSiteDeployments(site.name, env.name);
  const releases = useSiteReleases(site.name);
  // Only a kept release can be chosen (#2602): one the cleanup removed is gone.
  const options = useMemo(
    () => rollbackChoices(env, (deployments.data?.deployments ?? []).map((d) => d.to), releases.data?.releases),
    [deployments.data, releases.data, env],
  );
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
          {options.length === 0 && <p className="text-sm text-muted-foreground">{env.name} has served no earlier release it still keeps.</p>}
          {options.map((n) => (
            <label key={n} className="flex cursor-pointer items-start gap-2 rounded border p-3">
              <input type="radio" name="rollback" checked={chosen === n} onChange={() => setPicked(n)} className="mt-1" />
              <span>
                <span className="block font-medium">Release {n}</span>
                <span className="block text-xs text-muted-foreground">{note(n)}</span>
              </span>
            </label>
          ))}
          <p className="text-xs text-muted-foreground" data-testid="rollback-kept">
            {rollbackNote(env)}
          </p>
          <Failure text={failure} />
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
