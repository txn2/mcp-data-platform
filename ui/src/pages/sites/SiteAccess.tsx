import { useState } from "react";
import { Building2, User, Users } from "lucide-react";
import { useGrantSite, useRevokeSite, useSetSiteEnvironment, useSiteAccess, type GrantWho, type Site, type SiteGrant } from "@/api/portal/hooks/sites";
import { useDirectoryUsers } from "@/api/portal/hooks";
import { ExternalBadge } from "@/components/ExternalBadge";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { EVERYONE_INTERNAL, Failure, grantName } from "./SiteParts";
import { ShareDialog } from "./SiteShare";
import { day, errorText, orderedEnvironments } from "./siteFormat";

// SiteAccessTab is who reaches the site (#2559, #2604): the people who manage
// it, site-wide, and who each private environment is shared with. The owner
// and administrators add and remove people who manage; anyone who manages
// shares.
export function SiteAccessTab({ site }: { site: Site }) {
  const { data, isLoading, error } = useSiteAccess(site.name, true);
  const revoke = useRevokeSite(site.name);
  const [adding, setAdding] = useState(false);
  const [sharing, setSharing] = useState(false);
  const [changing, setChanging] = useState<SiteGrant | null>(null);
  const [failure, setFailure] = useState<string | null>(null);
  const own = site.permissions.own;
  const remove = (who: GrantWho) => {
    setFailure(null);
    revoke.mutate(who, { onError: (e) => setFailure(errorText(e, "This could not be removed")) });
  };
  if (error) {
    return (
      <Alert variant="destructive">
        <AlertDescription>{error.message}</AlertDescription>
      </Alert>
    );
  }
  if (isLoading || !data) return <p className="text-sm text-muted-foreground">Loading…</p>;
  const managers = data.grants.filter((g) => g.level === "manage");
  const shares = data.grants.filter((g) => g.level === "view");
  return (
    <>
      <Failure text={failure} />
      <SectionCard
        title="Who manages"
        action={
          <Button size="sm" variant="outline" onClick={() => setAdding(true)}>
            Add person
          </Button>
        }
      >
        <div className="space-y-3">
          <p className="text-sm text-muted-foreground">
            They publish to dev, promote, roll back, add domains and share. They can open every environment. Internal people only. Administrators can always manage this site.
          </p>
          <div className="overflow-x-auto">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Person</TableHead>
                  <TableHead>Access</TableHead>
                  <TableHead>Added</TableHead>
                  <TableHead />
                </TableRow>
              </TableHeader>
              <TableBody>
                <TableRow>
                  <TableCell className="break-all font-medium">{data.owner_email}</TableCell>
                  <TableCell>
                    <Badge variant="info">Owner</Badge>
                  </TableCell>
                  <TableCell>
                    <div>{day(site.created_at)}</div>
                    <div className="text-xs text-muted-foreground">Created the site</div>
                  </TableCell>
                  <TableCell />
                </TableRow>
                {managers.map((g) => (
                  <TableRow key={g.email}>
                    <TableCell className="break-all font-medium">{g.email}</TableCell>
                    <TableCell>
                      <Badge variant="muted">Can manage</Badge>
                    </TableCell>
                    <AddedCell grant={g} />
                    <TableCell>
                      {own && (
                        <div className="flex justify-end">
                          <Button size="sm" variant="outline" onClick={() => remove({ email: g.email })}>
                            Remove
                          </Button>
                        </div>
                      )}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
          {!own && <p className="text-xs text-muted-foreground">Only the owner and administrators add or remove people who manage.</p>}
        </div>
      </SectionCard>
      <SharedWith site={site} shares={shares} onShare={() => setSharing(true)} onChange={setChanging} onRemove={remove} onFail={setFailure} />
      {/* Add person and Share open the one dialog (#2587); Add person starts on Can manage where it is offered. */}
      {adding && <ShareDialog site={site} level="manage" onClose={() => setAdding(false)} />}
      {sharing && <ShareDialog site={site} onClose={() => setSharing(false)} />}
      {changing && <ChangeShareDialog site={site} grant={changing} onClose={() => setChanging(null)} />}
    </>
  );
}

function AddedCell({ grant }: { grant: SiteGrant }) {
  return (
    <TableCell>
      <div>{day(grant.created_at)}</div>
      <div className="text-xs text-muted-foreground">Added by {grant.granted_by}</div>
    </TableCell>
  );
}

function EnvBadges({ envs }: { envs: string[] }) {
  if (envs.length === 0) return <span className="text-xs text-muted-foreground">Every private environment</span>;
  return (
    <div className="flex flex-wrap gap-1.5">
      {envs.map((e) => (
        <Badge key={e} variant="muted">
          {e}
        </Badge>
      ))}
    </div>
  );
}

function SharedWith({
  site,
  shares,
  onShare,
  onChange,
  onRemove,
  onFail,
}: {
  site: Site;
  shares: SiteGrant[];
  onShare: () => void;
  onChange: (g: SiteGrant) => void;
  onRemove: (who: GrantWho) => void;
  onFail: (t: string) => void;
}) {
  const set = useSetSiteEnvironment(site.name);
  const internal = site.environments.filter((e) => e.internal_view && !e.public).map((e) => e.name);
  const publicEnvs = site.environments.filter((e) => e.public).map((e) => e.name);
  const stopInternal = async () => {
    try {
      for (const name of internal) await set.mutateAsync({ name, internal_view: false });
    } catch (e) {
      onFail(errorText(e, "Everyone internal could not be removed"));
    }
  };
  return (
    <SectionCard
      title="Shared with"
      action={
        <Button size="sm" onClick={onShare}>
          Share
        </Button>
      }
    >
      <div className="space-y-3">
        <p className="text-sm text-muted-foreground">
          Who else can open each private environment, after signing in. A persona share follows the persona: someone who joins it can open the environment on their next visit, and someone who leaves cannot.
          {publicEnvs.length > 0 && ` ${publicEnvs.join(", ")} ${publicEnvs.length === 1 ? "is" : "are"} public, so ${publicEnvs.length === 1 ? "it opens" : "they open"} for anyone and ${publicEnvs.length === 1 ? "is" : "are"} not shared.`}
        </p>
        {shares.length === 0 && internal.length === 0 ? (
          <p className="text-sm text-muted-foreground">Not shared with anyone yet.</p>
        ) : (
          <div className="overflow-x-auto">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Who</TableHead>
                  <TableHead>Environments</TableHead>
                  <TableHead>Added</TableHead>
                  <TableHead />
                </TableRow>
              </TableHeader>
              <TableBody>
                {shares.map((g) => (
                  <ShareRow key={grantName(g)} grant={g} onChange={() => onChange(g)} onRemove={() => onRemove(g.persona ? { persona: g.persona } : { email: g.email })} />
                ))}
                {internal.length > 0 && (
                  <TableRow>
                    <TableCell>
                      <Who icon="internal" name={EVERYONE_INTERNAL} kind="Anyone signed in whose persona is not external" />
                    </TableCell>
                    <TableCell>
                      <EnvBadges envs={internal} />
                    </TableCell>
                    <TableCell className="text-xs text-muted-foreground">Set in each environment's settings</TableCell>
                    <TableCell>
                      <div className="flex justify-end">
                        <Button size="sm" variant="outline" onClick={() => void stopInternal()} disabled={set.isPending}>
                          Remove
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                )}
              </TableBody>
            </Table>
          </div>
        )}
      </div>
    </SectionCard>
  );
}

function Who({ icon, name, kind, external }: { icon: "person" | "persona" | "internal"; name: string; kind: string; external?: boolean }) {
  const Icon = icon === "persona" ? Users : icon === "internal" ? Building2 : User;
  return (
    <div className="flex min-w-0 items-start gap-2">
      <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-muted" aria-hidden>
        <Icon className="h-4 w-4" />
      </span>
      <div className="min-w-0">
        <div className="flex flex-wrap items-center gap-1.5">
          <span className="break-all font-medium">{name}</span>
          {external && <ExternalBadge />}
        </div>
        <div className="text-xs text-muted-foreground">{kind}</div>
      </div>
    </div>
  );
}

function ShareRow({ grant: g, onChange, onRemove }: { grant: SiteGrant; onChange: () => void; onRemove: () => void }) {
  const { data } = useDirectoryUsers(g.email, !!g.email);
  const external = !!g.email && data?.users.some((u) => u.email.toLowerCase() === g.email && u.external);
  return (
    <TableRow>
      <TableCell>
        <Who icon={g.persona ? "persona" : "person"} name={grantName(g)} kind={g.persona ? "Persona" : "Person"} external={external} />
      </TableCell>
      <TableCell>
        <EnvBadges envs={g.environments} />
      </TableCell>
      <AddedCell grant={g} />
      <TableCell>
        <div className="flex justify-end gap-2">
          <Button size="sm" variant="outline" onClick={onChange}>
            Change
          </Button>
          <Button size="sm" variant="outline" onClick={onRemove}>
            Remove
          </Button>
        </div>
      </TableCell>
    </TableRow>
  );
}

// ChangeShareDialog changes which private environments a share opens.
function ChangeShareDialog({ site, grant: g, onClose }: { site: Site; grant: SiteGrant; onClose: () => void }) {
  const grant = useGrantSite(site.name);
  const privateEnvs = orderedEnvironments(site.environments).filter((e) => !e.public);
  const [envs, setEnvs] = useState<string[]>(g.environments.length ? g.environments : privateEnvs.map((e) => e.name));
  const [failure, setFailure] = useState<string | null>(null);
  const toggle = (name: string) => setEnvs((cur) => (cur.includes(name) ? cur.filter((e) => e !== name) : [...cur, name]));
  const submit = () => {
    setFailure(null);
    const who = g.persona ? { persona: g.persona } : { email: g.email };
    grant.mutate({ ...who, level: "view", environments: envs }, { onSuccess: onClose, onError: (e) => setFailure(errorText(e, "The share could not be changed")) });
  };
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Change what {grantName(g)} can open</DialogTitle>
          <DialogDescription>{site.name}</DialogDescription>
        </DialogHeader>
        <fieldset className="space-y-2">
          <legend className="mb-2 text-sm font-medium">Environments</legend>
          {privateEnvs.map((e) => (
            <label key={e.name} className="flex items-center gap-2 text-sm">
              <input type="checkbox" aria-label={e.name} checked={envs.includes(e.name)} onChange={() => toggle(e.name)} />
              {e.name}
            </label>
          ))}
        </fieldset>
        <Failure text={failure} />
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={envs.length === 0 || grant.isPending}>
            Save
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
