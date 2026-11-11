import { useMemo, useState } from "react";
import { X } from "lucide-react";
import { DEV_ENVIRONMENT, useGrantSite, useSiteAccess, type Site } from "@/api/portal/hooks/sites";
import { RecipientSearch, type Recipient } from "@/components/share/RecipientSearch";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Failure, grantOpens } from "./SiteParts";
import { bareAddress, envAddress, errorText, orderedEnvironments } from "./siteFormat";

// ShareDialog opens private environments to people and personas (#2604). It
// starts on the environment it was opened from; a public environment opens for
// anyone, so it is not offered.
export function ShareDialog({ site, env, onClose }: { site: Site; env?: string; onClose: () => void }) {
  const grant = useGrantSite(site.name);
  const access = useSiteAccess(site.name, true).data;
  // Any persona on the deployment can be given an environment, not only the
  // sharer's own, so the list comes from the site's access, not /share-personas.
  const personas = useMemo(() => (access?.personas ?? []).map((name) => ({ name, display_name: name })), [access]);
  const privateEnvs = orderedEnvironments(site.environments).filter((e) => !e.public);
  const [picked, setPicked] = useState<Recipient[]>([]);
  const [envs, setEnvs] = useState<string[]>(() => startingEnvs(site, env));
  const [notify, setNotify] = useState(true);
  const [failure, setFailure] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const pickedKeys = useMemo(() => new Set(picked.map((r) => r.key)), [picked]);
  // Someone already given every chosen environment is listed and not offered.
  const has = useMemo(() => {
    const out = new Set<string>();
    for (const g of access?.grants ?? []) {
      if (envs.length > 0 && envs.every((e) => grantOpens(g, e))) out.add(g.persona ? `g:${g.persona}` : `p:${g.email.toLowerCase()}`);
    }
    return out;
  }, [access, envs]);
  const people = picked.some((r) => r.kind !== "group");
  const persona = picked.find((r) => r.kind === "group");
  const toggle = (name: string) => setEnvs((cur) => (cur.includes(name) ? cur.filter((e) => e !== name) : [...cur, name]));
  const submit = async () => {
    setFailure(null);
    setBusy(true);
    try {
      for (const r of picked) {
        await grant.mutateAsync(
          r.kind === "group" ? { persona: r.persona, level: "view", environments: envs } : { email: r.email, level: "view", environments: envs, notify },
        );
      }
      onClose();
    } catch (e) {
      setFailure(errorText(e, "This could not be shared"));
    } finally {
      setBusy(false);
    }
  };
  const title = `Share ${envs.length === 1 ? envs[0] : site.name}`;
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-[560px]">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{site.name} · private, so people sign in with their platform account first</DialogDescription>
        </DialogHeader>
        <div className="space-y-4">
          <div className="space-y-2">
            <p className="text-sm font-medium">People or personas</p>
            <Picked picked={picked} onRemove={(key) => setPicked((cur) => cur.filter((x) => x.key !== key))} />
            <RecipientSearch personas={personas} has={has} picked={pickedKeys} onPick={(r) => setPicked((cur) => [...cur, r])} placeholder="Name, email or persona" />
          </div>
          <fieldset className="space-y-2">
            <legend className="mb-2 text-sm font-medium">Environments</legend>
            {privateEnvs.map((e) => {
              const addr = envAddress(site, e.name);
              const on = envs.includes(e.name);
              return (
                <label key={e.name} className={`flex cursor-pointer items-start gap-2 rounded border p-3 ${on ? "border-primary bg-primary/5" : ""}`}>
                  <input type="checkbox" className="mt-1" aria-label={e.name} checked={on} onChange={() => toggle(e.name)} />
                  <span className="min-w-0">
                    <span className="flex flex-wrap items-center gap-2">
                      <span className="font-medium">{e.name}</span>
                      {addr && <span className="break-all font-mono text-xs text-muted-foreground">{bareAddress(addr)}</span>}
                    </span>
                    <span className="block text-xs text-muted-foreground">
                      {e.name === DEV_ENVIRONMENT ? "Working copy. Usually only the people who manage the site open it." : "What people use. Changes by Promote and Roll back."}
                    </span>
                  </span>
                </label>
              );
            })}
            <p className="text-xs text-muted-foreground">Only private environments are listed. A public one opens for anyone.</p>
          </fieldset>
          {persona && <PersonaNote persona={persona} />}
          {people && (
            <label className="flex items-center gap-2 text-sm">
              <input type="checkbox" checked={notify} onChange={(e) => setNotify(e.target.checked)} />
              Email them the link
            </label>
          )}
          <Failure text={failure} />
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={() => void submit()} disabled={picked.length === 0 || envs.length === 0 || busy}>
            Share
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/** The environment a share starts on: the one it was opened from, else the first private one people use. */
function startingEnvs(site: Site, env?: string): string[] {
  if (env) return [env];
  const first = orderedEnvironments(site.environments).find((e) => !e.public && e.name !== DEV_ENVIRONMENT);
  return first ? [first.name] : [];
}

function Picked({ picked, onRemove }: { picked: Recipient[]; onRemove: (key: string) => void }) {
  if (picked.length === 0) return null;
  return (
    <div className="flex flex-wrap gap-1.5">
      {picked.map((r) => (
        <span key={r.key} className="inline-flex max-w-full items-center gap-1.5 rounded-full border bg-card px-2.5 py-0.5 text-xs">
          <span className="break-all">{r.name}</span>
          <button type="button" aria-label={`Remove ${r.name}`} className="inline-flex" onClick={() => onRemove(r.key)}>
            <X className="h-3 w-3" aria-hidden />
          </button>
        </span>
      ))}
    </div>
  );
}

function PersonaNote({ persona }: { persona: Recipient }) {
  const count = persona.count === undefined ? "" : ` has ${persona.count} ${persona.count === 1 ? "person" : "people"} today`;
  return (
    <Alert>
      <AlertTitle>
        {persona.name}
        {count}
      </AlertTitle>
      <AlertDescription>
        Whoever holds this persona when they visit can open what you share. Someone added to it gets in on their next visit; someone removed is refused on theirs.
      </AlertDescription>
    </Alert>
  );
}
