import { useState } from "react";
import { ExternalLink } from "lucide-react";
import {
  useAddDeploymentDomain,
  useDeploymentAddresses,
  useRemoveDeploymentDomain,
  type DeploymentAddress,
} from "@/api/admin/hooks";
import { useApplications } from "@/api/portal/hooks/applications";
import { DEV_ENVIRONMENT, useSites } from "@/api/portal/hooks/sites";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { DOMAIN_NOTE } from "./SiteDomains";
import { errorText, orderedEnvironments, when } from "./siteFormat";

const KIND_LABEL: Record<DeploymentAddress["kind"], string> = { site_address: "Site address", domain: "Domain", application: "Application" };

// AdminDomainsPage is every hostname the deployment answers on for a site or an
// application (#2559): what a system administrator adds ingress rules for.
export function AdminDomainsPage({ onNavigate }: { onNavigate: (path: string) => void }) {
  const { data, isLoading, error } = useDeploymentAddresses();
  const remove = useRemoveDeploymentDomain();
  const [adding, setAdding] = useState(false);
  const [removing, setRemoving] = useState<string | null>(null);
  const [failure, setFailure] = useState<string | null>(null);
  const confirm = () => {
    setFailure(null);
    remove.mutate(removing!, {
      onSuccess: () => setRemoving(null),
      onError: (e) => setFailure(errorText(e, "This domain could not be removed")),
    });
  };
  const serves = (a: DeploymentAddress) => (a.application ? a.application : `${a.site} / ${a.environment}`);
  const open = (a: DeploymentAddress) =>
    onNavigate(a.application ? `/applications/${encodeURIComponent(a.application)}` : `/sites/${encodeURIComponent(a.site ?? "")}`);
  return (
    <div className="space-y-4">
      <SectionCard
        title="Domains"
        action={
          <Button size="sm" onClick={() => setAdding(true)}>
            Add domain
          </Button>
        }
      >
        {error ? (
          <Alert variant="destructive">
            <AlertDescription>{error.message}</AlertDescription>
          </Alert>
        ) : isLoading ? (
          <p className="text-sm text-muted-foreground">Loading…</p>
        ) : (
          <div className="overflow-x-auto">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Domain</TableHead>
                  <TableHead>Serves</TableHead>
                  <TableHead>Added</TableHead>
                  <TableHead />
                </TableRow>
              </TableHeader>
              <TableBody>
                {(data?.addresses ?? []).map((a) => (
                  <TableRow key={a.hostname}>
                    <TableCell className="break-all">
                      <a className="inline-flex items-center gap-1 font-medium hover:underline" href={`https://${a.hostname}`} target="_blank" rel="noreferrer" title="Open in a new tab">
                        {a.hostname}
                        <ExternalLink className="h-3 w-3 shrink-0" aria-hidden />
                      </a>
                    </TableCell>
                    <TableCell>
                      <button type="button" className="text-primary hover:underline" onClick={() => open(a)}>
                        {serves(a)}
                      </button>
                      <p className="text-xs text-muted-foreground">{KIND_LABEL[a.kind]}</p>
                    </TableCell>
                    <TableCell>
                      <div>{when(a.created_at)}</div>
                      <div className="text-xs text-muted-foreground">{a.created_by}</div>
                    </TableCell>
                    <TableCell>
                      {a.kind !== "site_address" && (
                        <div className="flex justify-end">
                          <Button size="sm" variant="outline" onClick={() => setRemoving(a.hostname)}>
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
        )}
        <p className="mt-3 text-xs text-muted-foreground">
          Each of these needs an ingress rule, added by a system administrator. A site's own address needs a sign-in, and any other domain is public unless the site asks for one.
        </p>
      </SectionCard>
      {adding && <AdminAddDomainDialog onClose={() => setAdding(false)} />}
      {removing && (
        <Dialog open onOpenChange={(o) => !o && setRemoving(null)}>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>Remove {removing}</DialogTitle>
            </DialogHeader>
            <p className="text-sm text-muted-foreground">It stops serving as soon as it is removed. What it served does not change.</p>
            {failure && (
              <Alert variant="destructive">
                <AlertDescription>{failure}</AlertDescription>
              </Alert>
            )}
            <DialogFooter>
              <Button variant="outline" onClick={() => setRemoving(null)}>
                Cancel
              </Button>
              <Button onClick={confirm} disabled={remove.isPending}>
                Remove domain
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      )}
    </div>
  );
}

function AdminAddDomainDialog({ onClose }: { onClose: () => void }) {
  const add = useAddDeploymentDomain();
  const [hostname, setHostname] = useState("");
  const [target, setTarget] = useState<DomainTarget>({ serves: "site", site: "", environment: "", application: "" });
  const [failure, setFailure] = useState<string | null>(null);
  const body = target.serves === "site" ? { site: target.site, environment: target.environment } : { application: target.application };
  const ready = hostname.trim() !== "" && Object.values(body).every(Boolean);
  const submit = () => {
    setFailure(null);
    add.mutate({ hostname: hostname.trim(), ...body }, { onSuccess: onClose, onError: (e) => setFailure(errorText(e, "This domain could not be added")) });
  };
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Add domain</DialogTitle>
        </DialogHeader>
        <div className="space-y-3">
          {failure && (
            <Alert variant="destructive">
              <AlertTitle>Not added</AlertTitle>
              <AlertDescription>{failure}</AlertDescription>
            </Alert>
          )}
          <div className="space-y-1">
            <Label htmlFor="admin-domain">Domain</Label>
            <Input id="admin-domain" placeholder="shop.example.com" value={hostname} onChange={(e) => setHostname(e.target.value)} />
            <p className="text-xs text-muted-foreground">Not the portal's own hostname, anything under the pages domain, or anything under the portal session's domain.</p>
          </div>
          <ServesPicker target={target} onChange={setTarget} />
          <p className="text-xs text-muted-foreground">{DOMAIN_NOTE} A domain answers with its environment's access.</p>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={!ready || add.isPending}>
            Add domain
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

interface DomainTarget {
  serves: "site" | "application";
  site: string;
  environment: string;
  application: string;
}

function ServesPicker({ target, onChange }: { target: DomainTarget; onChange: (t: DomainTarget) => void }) {
  const set = (patch: Partial<DomainTarget>) => onChange({ ...target, ...patch });
  return (
    <div className="space-y-2">
      <Label>Serves</Label>
      <label className="flex items-start gap-2 rounded border p-3">
        <input type="radio" name="serves" aria-label="A site environment" className="mt-1" checked={target.serves === "site"} onChange={() => set({ serves: "site" })} />
        <span className="min-w-0 flex-1 space-y-2">
          <span className="block font-medium">A site environment</span>
          {target.serves === "site" && <SiteTargetFields target={target} onChange={set} />}
        </span>
      </label>
      <label className="flex items-start gap-2 rounded border p-3">
        <input type="radio" name="serves" aria-label="An application" className="mt-1" checked={target.serves === "application"} onChange={() => set({ serves: "application" })} />
        <span className="min-w-0 flex-1 space-y-2">
          <span className="block font-medium">An application</span>
          <span className="block text-xs text-muted-foreground">Its pages and endpoints answer on this domain.</span>
          {target.serves === "application" && <ApplicationField value={target.application} onChange={(application) => set({ application })} />}
        </span>
      </label>
    </div>
  );
}

function SiteTargetFields({ target, onChange }: { target: DomainTarget; onChange: (p: Partial<DomainTarget>) => void }) {
  const sites = useSites().data?.sites ?? [];
  const envs = orderedEnvironments(sites.find((s) => s.name === target.site)?.environments ?? []).filter((e) => e.name !== DEV_ENVIRONMENT);
  return (
    <span className="grid gap-2 sm:grid-cols-2">
      <Select value={target.site} onValueChange={(site) => onChange({ site, environment: "" })}>
        <SelectTrigger aria-label="Site">
          <SelectValue placeholder="Site" />
        </SelectTrigger>
        <SelectContent>
          {sites.map((s) => (
            <SelectItem key={s.id} value={s.name}>
              {s.name}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      <Select value={target.environment} onValueChange={(environment) => onChange({ environment })}>
        <SelectTrigger aria-label="Environment">
          <SelectValue placeholder="Environment" />
        </SelectTrigger>
        <SelectContent>
          {envs.map((e) => (
            <SelectItem key={e.name} value={e.name}>
              {e.name}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </span>
  );
}

function ApplicationField({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  const applications = useApplications().data?.applications ?? [];
  return (
    <Select value={value} onValueChange={onChange}>
      <SelectTrigger aria-label="Application">
        <SelectValue placeholder="Application" />
      </SelectTrigger>
      <SelectContent>
        {applications.map((a) => (
          <SelectItem key={a.id} value={a.name}>
            {a.name}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}
