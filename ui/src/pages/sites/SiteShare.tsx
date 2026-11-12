import { useEffect, useMemo, useState } from "react";
import { Lock, X } from "lucide-react";
import { DEV_ENVIRONMENT, useGrantSite, useSiteAccess, type Site, type SiteGrant } from "@/api/portal/hooks/sites";
import { useDirectoryUsers } from "@/api/portal/hooks";
import { ExternalBadge } from "@/components/ExternalBadge";
import { RecipientSearch, type Recipient } from "@/components/share/RecipientSearch";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Failure, grantOpens } from "./SiteParts";
import { bareAddress, envAddress, errorText, orderedEnvironments } from "./siteFormat";

// ShareDialog is the one way to give someone access to a site (#2587, #2604):
// Share on an environment and Add person on the Access tab both open it. Can
// view opens the private environments picked; Can manage covers every
// environment and lets them publish, promote and share. Can manage stays in
// view, unavailable and saying why, for someone who manages without owning, a
// persona and an external person: who may grant what is the server's rule, and
// it still refuses what this would not offer.

export type ShareLevel = SiteGrant["level"];

const MANAGE_HELP = "Publishes to dev, promotes, rolls back, adds domains and shares. Opens every environment. Internal people only.";
const NOT_OWNER = "Only the site’s owner or an administrator can add someone who manages it.";
const PERSONA_VIEW_ONLY = "A persona can be given Can view only. Can manage is for a person.";

/** useShare is who is picked, what they get, and the grants a Share makes. */
function useShare(site: Site, startLevel: ShareLevel, env: string | undefined, onClose: () => void) {
  const grant = useGrantSite(site.name);
  const access = useSiteAccess(site.name, true).data;
  const [picked, setPicked] = useState<Recipient[]>([]);
  const [externals, setExternals] = useState<Set<string>>(new Set());
  const [chosen, setChosen] = useState<ShareLevel>(startLevel);
  const [envs, setEnvs] = useState<string[]>(() => startingEnvs(site, env));
  const [notify, setNotify] = useState(true);
  const [failure, setFailure] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const persona = picked.find((r) => r.kind === "group");
  const manageWhy = whyNotManage(site, persona, picked.find((r) => externals.has(r.key)));
  const level: ShareLevel = manageWhy ? "view" : chosen;
  const markExternal = (key: string, isExternal: boolean) =>
    setExternals((cur) => {
      if (cur.has(key) === isExternal) return cur;
      const next = new Set(cur);
      if (isExternal) next.add(key);
      else next.delete(key);
      return next;
    });
  const submit = async () => {
    setFailure(null);
    setBusy(true);
    try {
      for (const r of picked) await grant.mutateAsync(grantBody(r, level, envs, notify));
      onClose();
    } catch (e) {
      setFailure(errorText(e, "This could not be shared"));
    } finally {
      setBusy(false);
    }
  };
  return {
    access, picked, setPicked, markExternal, persona, manageWhy, level, setChosen, envs, notify, setNotify, failure, busy, submit,
    toggle: (name: string) => setEnvs((cur) => (cur.includes(name) ? cur.filter((e) => e !== name) : [...cur, name])),
    ready: picked.length > 0 && (level === "manage" || envs.length > 0),
  };
}

export function ShareDialog({ site, env, level: startLevel = "view", onClose }: { site: Site; env?: string; level?: ShareLevel; onClose: () => void }) {
  const sh = useShare(site, startLevel, env, onClose);
  // Any persona on the deployment can be given an environment, not only the
  // sharer's own, so the list comes from the site's access, not /share-personas.
  const personas = useMemo(() => (sh.access?.personas ?? []).map((name) => ({ name, display_name: name })), [sh.access]);
  const has = useHasAccess(sh.access?.grants ?? [], sh.envs, sh.level);
  const pickedKeys = useMemo(() => new Set(sh.picked.map((r) => r.key)), [sh.picked]);
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-h-[90dvh] overflow-y-auto sm:max-w-[560px]">
        <DialogHeader>
          <DialogTitle>Share {site.name}</DialogTitle>
          <DialogDescription>People sign in with their platform account first</DialogDescription>
        </DialogHeader>
        <div className="min-w-0 space-y-4">
          <div className="space-y-2">
            <p className="text-sm font-medium">People or personas</p>
            <Picked picked={sh.picked} onRemove={(key) => sh.setPicked((cur) => cur.filter((x) => x.key !== key))} onExternal={sh.markExternal} />
            <RecipientSearch personas={personas} has={has} picked={pickedKeys} onPick={(r) => sh.setPicked((cur) => [...cur, r])} placeholder="Name, email or persona" />
          </div>
          <LevelChoice level={sh.level} onChange={sh.setChosen} manageWhy={sh.manageWhy} />
          <ShareScope site={site} sh={sh} />
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={() => void sh.submit()} disabled={!sh.ready || sh.busy}>
            Share
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// ShareScope is what the share covers: one sentence for Can manage, the
// private environments to pick for Can view, and where a refusal shows.
function ShareScope({ site, sh }: { site: Site; sh: ReturnType<typeof useShare> }) {
  const privateEnvs = orderedEnvironments(site.environments);
  const people = sh.picked.some((r) => r.kind !== "group");
  if (sh.level === "manage") {
    return (
      <>
        <Failure text={sh.failure} testId="share-failure" />
        <p className="text-sm text-muted-foreground" data-testid="manage-covers">
          Can manage covers every environment, so there is nothing to pick. They can also share the site with others.
        </p>
        <NotifyCheck show={people} checked={sh.notify} onChange={sh.setNotify} />
      </>
    );
  }
  return (
    <>
      <EnvironmentPick site={site} envs={privateEnvs.map((e) => e.name)} chosen={sh.envs} onToggle={sh.toggle} />
      {sh.persona && <PersonaNote persona={sh.persona} />}
      <NotifyCheck show={people} checked={sh.notify} onChange={sh.setNotify} />
      <Failure text={sh.failure} testId="share-failure" />
    </>
  );
}

function NotifyCheck({ show, checked, onChange }: { show: boolean; checked: boolean; onChange: (v: boolean) => void }) {
  if (!show) return null;
  return (
    <label className="flex items-center gap-2 text-sm">
      <input type="checkbox" checked={checked} onChange={(e) => onChange(e.target.checked)} />
      Email them the link
    </label>
  );
}

/** Why Can manage is unavailable to this sharer for these people, "" when it is offered. */
function whyNotManage(site: Site, persona?: Recipient, external?: Recipient): string {
  if (!site.permissions.own) return NOT_OWNER;
  if (persona) return PERSONA_VIEW_ONLY;
  if (external) return `${external.email ?? external.name} is external. Only internal people can manage a site.`;
  return "";
}

function grantBody(r: Recipient, level: ShareLevel, envs: string[], notify: boolean) {
  if (r.kind === "group") return { persona: r.persona, level: "view" as const, environments: envs };
  if (level === "manage") return { email: r.email, level, notify };
  return { email: r.email, level, environments: envs, notify };
}

/** Who already has what this share would give, and is listed rather than offered. */
function useHasAccess(grants: SiteGrant[], envs: string[], level: ShareLevel) {
  return useMemo(() => {
    const out = new Set<string>();
    for (const g of grants) {
      const covered = level === "manage" ? g.level === "manage" : g.level === "manage" || (envs.length > 0 && envs.every((e) => grantOpens(g, e)));
      if (covered) out.add(g.persona ? `g:${g.persona}` : `p:${g.email.toLowerCase()}`);
    }
    return out;
  }, [grants, envs, level]);
}

function LevelChoice({ level, onChange, manageWhy }: { level: ShareLevel; onChange: (l: ShareLevel) => void; manageWhy: string }) {
  return (
    <fieldset className="space-y-2">
      <legend className="mb-2 text-sm font-medium">Access</legend>
      <Choice name="share-level" title="Can view" help="Opens the environments you pick, after signing in." checked={level === "view"} onPick={() => onChange("view")} />
      <Choice name="share-level" title="Can manage" help={MANAGE_HELP} checked={level === "manage"} onPick={() => onChange("manage")} disabled={!!manageWhy}>
        {manageWhy && (
          <span className="mt-1 flex items-start gap-1.5 text-xs text-muted-foreground" data-testid="manage-why">
            <Lock className="mt-0.5 h-3 w-3 shrink-0" aria-hidden />
            {manageWhy}
          </span>
        )}
      </Choice>
    </fieldset>
  );
}

function Choice({
  name,
  title,
  help,
  checked,
  onPick,
  disabled = false,
  children,
}: {
  name: string;
  title: string;
  help: string;
  checked: boolean;
  onPick: () => void;
  disabled?: boolean;
  children?: React.ReactNode;
}) {
  return (
    <label className={`flex items-start gap-2 rounded border p-3 ${checked ? "border-primary bg-primary/5" : ""} ${disabled ? "opacity-70" : "cursor-pointer"}`}>
      <input type="radio" name={name} aria-label={title} className="mt-1" checked={checked} disabled={disabled} onChange={onPick} />
      <span className="min-w-0">
        <span className="block font-medium">{title}</span>
        <span className="block text-xs text-muted-foreground">{help}</span>
        {children}
      </span>
    </label>
  );
}

function EnvironmentPick({ site, envs, chosen, onToggle }: { site: Site; envs: string[]; chosen: string[]; onToggle: (name: string) => void }) {
  const publicDomains = site.domains.filter((d) => d.public && envs.includes(d.environment ?? "")).map((d) => d.hostname);
  return (
    <fieldset className="space-y-2">
      <legend className="mb-2 text-sm font-medium">Environments</legend>
      {envs.map((name) => {
        const addr = envAddress(site, name);
        const on = chosen.includes(name);
        return (
          <label key={name} className={`flex cursor-pointer items-start gap-2 rounded border p-3 ${on ? "border-primary bg-primary/5" : ""}`}>
            <input type="checkbox" className="mt-1" aria-label={name} checked={on} onChange={() => onToggle(name)} />
            <span className="min-w-0">
              <span className="flex flex-wrap items-center gap-2">
                <span className="font-medium">{name}</span>
                {addr && <span className="break-all font-mono text-xs text-muted-foreground">{bareAddress(addr)}</span>}
              </span>
              <span className="block text-xs text-muted-foreground">
                {name === DEV_ENVIRONMENT ? "Working copy. Usually only the people who manage the site open it." : "What people use. Changes by Promote and Roll back."}
              </span>
            </span>
          </label>
        );
      })}
      <p className="text-xs text-muted-foreground">
        A share opens an environment's addresses that ask for a sign-in.
        {publicDomains.length > 0 && ` ${publicDomains.join(", ")} ${publicDomains.length === 1 ? "answers" : "answer"} anyone without one.`}
      </p>
    </fieldset>
  );
}

/** The environment a share starts on: the one it was opened from, else the first private one people use. */
function startingEnvs(site: Site, env?: string): string[] {
  if (env) return [env];
  const first = orderedEnvironments(site.environments).find((e) => e.name !== DEV_ENVIRONMENT);
  return first ? [first.name] : [];
}

function Picked({ picked, onRemove, onExternal }: { picked: Recipient[]; onRemove: (key: string) => void; onExternal: (key: string, external: boolean) => void }) {
  if (picked.length === 0) return null;
  return (
    <div className="flex flex-wrap gap-1.5">
      {picked.map((r) => (
        <PickedChip key={r.key} recipient={r} onRemove={() => onRemove(r.key)} onExternal={(x) => onExternal(r.key, x)} />
      ))}
    </div>
  );
}

// PickedChip names one person or persona, and asks the directory whether a
// person is external, which Can manage is refused to.
function PickedChip({ recipient: r, onRemove, onExternal }: { recipient: Recipient; onRemove: () => void; onExternal: (external: boolean) => void }) {
  const email = r.kind === "group" ? "" : (r.email ?? "").toLowerCase();
  const { data } = useDirectoryUsers(email, !!email);
  const external = !!email && !!data?.users.some((u) => u.email.toLowerCase() === email && u.external);
  useEffect(() => onExternal(external), [external]); // eslint-disable-line react-hooks/exhaustive-deps -- report a change, not a new callback
  return (
    <span className="inline-flex max-w-full items-center gap-1.5 rounded-full border bg-card px-2.5 py-0.5 text-xs" data-testid="share-picked">
      <span className="break-all">{r.name}</span>
      {external && <ExternalBadge />}
      <button type="button" aria-label={`Remove ${r.name}`} className="inline-flex" onClick={onRemove}>
        <X className="h-3 w-3" aria-hidden />
      </button>
    </span>
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
