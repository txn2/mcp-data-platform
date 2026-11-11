import { useState } from "react";
import { DEV_ENVIRONMENT, useSetMainEnvironment, useSetSiteEnvironment, useSites, type Site, type SiteEnvironment } from "@/api/portal/hooks/sites";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { useAuthStore } from "@/stores/auth";
import { AddDomainDialog } from "./SiteDomains";
import { envNameProblem } from "./envName";
import { AccessBadge, Failure } from "./SiteParts";
import { PromoteDialog, RollbackDialog } from "./SitePromote";
import { ReleasesKeptSection, keepBody, keepChoiceOf, type KeepChoice } from "./SiteRetention";
import { PublishDialog } from "./SitePublish";
import { JobDialog } from "./SitePublishJob";
import { ShareDialog } from "./SiteShare";
import { bareAddress, envAddress, errorText, hostsOf, orderedEnvironments } from "./siteFormat";

export type EnvDialog =
  | { kind: "promote" | "rollback" | "share" | "settings"; env: SiteEnvironment }
  | { kind: "publish" | "domain" | "new" | "main" }
  | { kind: "job"; id: string }
  | null;

export function EnvironmentDialogs({ site, dialog, onClose, onNavigate }: { site: Site; dialog: EnvDialog; onClose: () => void; onNavigate: (path: string) => void }) {
  if (dialog && "env" in dialog) return <EnvironmentDialog site={site} dialog={dialog} onClose={onClose} />;
  switch (dialog?.kind) {
    case "publish":
      return <PublishDialog site={site} onClose={onClose} onNavigate={onNavigate} />;
    case "domain":
      return <AddDomainDialog site={site} onClose={onClose} />;
    case "new":
      return <NewEnvironmentDialog site={site} onClose={onClose} />;
    case "main":
      return <MainAddressDialog site={site} onClose={onClose} />;
    case "job":
      return <JobDialog site={site} jobId={dialog.id} onClose={onClose} onNavigate={onNavigate} />;
    default:
      return null;
  }
}

// EnvironmentDialog is a dialog about one environment.
function EnvironmentDialog({ site, dialog, onClose }: { site: Site; dialog: { kind: "promote" | "rollback" | "share" | "settings"; env: SiteEnvironment }; onClose: () => void }) {
  switch (dialog.kind) {
    case "share":
      return <ShareDialog site={site} env={dialog.env.name} onClose={onClose} />;
    case "settings":
      return <EnvironmentSettingsDialog site={site} env={dialog.env} onClose={onClose} />;
    case "promote":
      return <PromoteDialog site={site} target={dialog.env} onClose={onClose} />;
    case "rollback":
      return <RollbackDialog site={site} env={dialog.env} onClose={onClose} />;
  }
}

function AccessChoice({ isPublic, onChange, canPublic, publicHelp }: { isPublic: boolean; onChange: (p: boolean) => void; canPublic: boolean; publicHelp: string }) {
  const pick = (value: boolean, title: string, help: string, disabled = false) => (
    <label className={`flex items-start gap-2 rounded border p-3 ${isPublic === value ? "border-primary bg-primary/5" : ""} ${disabled ? "opacity-60" : "cursor-pointer"}`}>
      <input type="radio" name="access" aria-label={title} className="mt-1" checked={isPublic === value} disabled={disabled} onChange={() => onChange(value)} />
      <span>
        <span className="block font-medium">{title}</span>
        <span className="block text-xs text-muted-foreground">{help}</span>
      </span>
    </label>
  );
  return (
    <fieldset className="space-y-2">
      <legend className="mb-2 text-sm font-medium">Access</legend>
      {pick(false, "Private", "Opens only for the people and personas you share it with, after they sign in. You can share it next.")}
      {pick(true, "Public", publicHelp, !canPublic)}
    </fieldset>
  );
}

function ApprovalCheck({ checked, onChange, help }: { checked: boolean; onChange: (v: boolean) => void; help: string }) {
  return (
    <label className="flex items-start gap-2">
      <input type="checkbox" className="mt-1" checked={checked} onChange={(e) => onChange(e.target.checked)} />
      <span>
        <span className="block font-medium">Requires approval</span>
        <span className="block text-xs text-muted-foreground">{help}</span>
      </span>
    </label>
  );
}

const APPROVAL_HELP = "A release reaches it only by Promote, and someone who is not an administrator has to ask first.";

export function NewEnvironmentDialog({ site, onClose }: { site: Site; onClose: () => void }) {
  const set = useSetSiteEnvironment(site.name);
  const pagesDomain = useSites().data?.pages_domain ?? "";
  const [name, setName] = useState("");
  const [isPublic, setPublic] = useState(false);
  const [approval, setApproval] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const trimmed = name.trim();
  const problem = envNameProblem(site.name, trimmed);
  const taken = site.environments.some((e) => e.name === trimmed);
  const submit = () => {
    setFailure(null);
    set.mutate(
      { name: trimmed, public: isPublic, requires_approval: approval },
      { onSuccess: onClose, onError: (e) => setFailure(errorText(e, "The environment could not be created")) },
    );
  };
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>New environment</DialogTitle>
          <DialogDescription>{site.name}</DialogDescription>
        </DialogHeader>
        <div className="space-y-3">
          <div className="space-y-1">
            <Label htmlFor="env-name">Name</Label>
            <Input id="env-name" value={name} onChange={(e) => setName(e.target.value)} aria-invalid={!!problem || taken} autoFocus />
            <p className="text-xs text-muted-foreground">Lowercase letters, digits and single hyphens, up to 32. It is part of the address, so it does not change.</p>
          </div>
          <NewAddress site={site.name} name={trimmed} pagesDomain={pagesDomain} problem={problem || (taken ? `${site.name} already has an environment named ${trimmed}.` : "")} />
          <AccessChoice isPublic={isPublic} onChange={setPublic} canPublic={site.permissions.own} publicHelp="Opens for anyone, with no sign-in. Only the owner or an administrator can choose this." />
          <ApprovalCheck checked={approval} onChange={setApproval} help={APPROVAL_HELP} />
          <p className="text-xs text-muted-foreground">It starts with no release. Promote one from dev when it is ready.</p>
          <Failure text={failure} />
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={!trimmed || !!problem || taken || set.isPending}>
            Create environment
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function NewAddress({ site, name, pagesDomain, problem }: { site: string; name: string; pagesDomain: string; problem: string }) {
  return (
    <>
      {pagesDomain && (
        <div className="space-y-1">
          <p className="text-sm font-medium">Address</p>
          <p className="break-all rounded bg-muted px-3 py-2 font-mono text-xs">
            {site}--{name || "name"}.{pagesDomain}
          </p>
        </div>
      )}
      {problem && (
        <p className="text-xs text-destructive" data-testid="env-name-problem">
          {problem}
        </p>
      )}
    </>
  );
}

// EnvironmentSettingsDialog changes an environment's access and approval rule.
// Choosing Public opens the confirmation instead of saving, so nothing becomes
// public without the person reading what does.
export function EnvironmentSettingsDialog({ site, env, onClose }: { site: Site; env: SiteEnvironment; onClose: () => void }) {
  const set = useSetSiteEnvironment(site.name);
  const [internal, setInternal] = useState(env.internal_view);
  const [approval, setApproval] = useState(env.requires_approval);
  const [makePublic, setMakePublic] = useState(false);
  const [makePrivate, setMakePrivate] = useState(false);
  const [keep, setKeep] = useState<KeepChoice>(() => keepChoiceOf(env));
  const [failure, setFailure] = useState<string | null>(null);
  const working = env.name === DEV_ENVIRONMENT;
  if (makePublic) return <MakePublicDialog site={site} env={env} onClose={onClose} />;
  const keepReleases = site.permissions.own ? keepBody(env, keep) : undefined;
  const submit = () => {
    setFailure(null);
    const body = {
      name: env.name,
      internal_view: internal,
      ...(working ? {} : { requires_approval: approval }),
      ...(makePrivate ? { public: false } : {}),
      ...(keepReleases !== undefined ? { keep_releases: keepReleases } : {}),
    };
    set.mutate(body, { onSuccess: onClose, onError: (e) => setFailure(errorText(e, "The settings could not be saved")) });
  };
  const isPublic = env.public && !makePrivate;
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-h-[90dvh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{env.name} settings</DialogTitle>
          <DialogDescription>{site.name}</DialogDescription>
        </DialogHeader>
        <div className="space-y-3">
          {working ? (
            <div className="space-y-1">
              <p className="text-sm font-medium">Access</p>
              <AccessBadge env={env} />
              <p className="text-xs text-muted-foreground">dev is the working copy, so it is always private. Only the people who manage this site and administrators open it, unless you share it.</p>
            </div>
          ) : (
            <AccessChoice
              isPublic={isPublic}
              onChange={(p) => (p ? setMakePublic(true) : setMakePrivate(env.public))}
              canPublic={site.permissions.own}
              publicHelp="Opens for anyone, with no sign-in. You confirm what becomes public before anything changes. Only the owner or an administrator can choose this."
            />
          )}
          {!isPublic && (
            <label className="flex items-start gap-2">
              <input type="checkbox" className="mt-1" checked={internal} onChange={(e) => setInternal(e.target.checked)} />
              <span>
                <span className="block font-medium">Everyone internal can open</span>
                <span className="block text-xs text-muted-foreground">Anyone signed in to this platform whose persona is not external, as well as whoever {env.name} is shared with.</span>
              </span>
            </label>
          )}
          {working ? (
            <>
              <SettingNote title="Requires approval" text="Publishing and editing land in dev directly, so dev never requires approval." />
              <SettingNote title="Domains" text="dev can't have a domain. It is the working copy, not an address people rely on." />
            </>
          ) : (
            <ApprovalCheck checked={approval} onChange={setApproval} help={`${APPROVAL_HELP} Making ${env.name} public needs the same approval.`} />
          )}
          {site.main_environment === env.name && site.address && (
            <SettingNote title="Main address" text={`${env.name} serves the main address, ${bareAddress(site.address)}. Change that with Main address on the Environments tab.`} />
          )}
          <ReleasesKeptSection site={site} env={env} choice={keep} onChange={setKeep} />
          <Failure text={failure} />
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={set.isPending}>
            Save
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function SettingNote({ title, text }: { title: string; text: string }) {
  return (
    <div className="space-y-1">
      <p className="text-sm font-medium">{title}</p>
      <p className="text-xs text-muted-foreground">{text}</p>
    </div>
  );
}

// MakePublicDialog names every address that would open to anyone before it
// changes anything (#2604). The owner or an administrator confirms; on an
// environment that requires approval, someone who is not an administrator asks.
export function MakePublicDialog({ site, env, onClose }: { site: Site; env: SiteEnvironment; onClose: () => void }) {
  const isAdmin = useAuthStore((s) => s.isAdmin());
  const set = useSetSiteEnvironment(site.name);
  const [typed, setTyped] = useState("");
  const [reason, setReason] = useState("");
  const [failure, setFailure] = useState<string | null>(null);
  const hosts = [envAddress(site, env.name), ...hostsOf(site, env.name).map((d) => d.hostname)].filter(Boolean).map(bareAddress);
  const asks = env.requires_approval && !isAdmin;
  const submit = () => {
    setFailure(null);
    set.mutate(
      { name: env.name, public: true, ...(asks ? { reason: reason.trim() } : {}) },
      { onSuccess: onClose, onError: (e) => setFailure(errorText(e, "The environment could not be made public")) },
    );
  };
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Make {env.name} public?</DialogTitle>
          <DialogDescription>{site.name}</DialogDescription>
        </DialogHeader>
        {site.permissions.own ? (
          <div className="space-y-3">
            <div className="space-y-1">
              <p className="text-sm font-medium">Anyone on the internet can open these, without signing in</p>
              {hosts.map((h) => (
                <p key={h} className="break-all font-mono text-sm">
                  {h}
                </p>
              ))}
            </div>
            <ul className="list-disc space-y-1 pl-5 text-sm">
              <li>The platform stops asking for a sign-in there. If the site needs one, it has to provide its own.</li>
              <li>{env.release > 0 ? `Release ${env.release}, and every release` : "Every release"} promoted to {env.name} from now on, is public.</li>
              <li>The shares on {env.name} stop mattering while it is public. They are kept, and apply again if you make it private.</li>
            </ul>
            {asks ? (
              <>
                <div className="space-y-1">
                  <Label htmlFor="public-reason">Why make it public</Label>
                  <Textarea id="public-reason" value={reason} onChange={(e) => setReason(e.target.value)} />
                </div>
                <p className="text-xs text-muted-foreground">{env.name} requires approval. This sends a request to Approvals, and {env.name} stays private until it is approved and you finish.</p>
              </>
            ) : (
              <div className="space-y-1">
                <Label htmlFor="public-confirm">Type {env.name} to confirm</Label>
                <Input id="public-confirm" autoComplete="off" value={typed} onChange={(e) => setTyped(e.target.value)} />
              </div>
            )}
            <Failure text={failure} />
          </div>
        ) : (
          <Alert variant="destructive">
            <AlertTitle>Only the owner or an administrator can make an environment public</AlertTitle>
            <AlertDescription>
              {site.owner_email} owns {site.name}. You can share {env.name} with more people instead, or ask the owner.
            </AlertDescription>
          </Alert>
        )}
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            {site.permissions.own ? "Cancel" : "Close"}
          </Button>
          {site.permissions.own &&
            (asks ? (
              <Button onClick={submit} disabled={set.isPending}>
                Ask for approval
              </Button>
            ) : (
              <Button variant="destructive" onClick={submit} disabled={typed !== env.name || set.isPending}>
                Make {env.name} public
              </Button>
            ))}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// MainAddressDialog moves the site's main address to another environment, or
// to none, and says what each address serves afterwards (#2604).
export function MainAddressDialog({ site, onClose }: { site: Site; onClose: () => void }) {
  const move = useSetMainEnvironment(site.name);
  const pagesDomain = useSites().data?.pages_domain ?? "";
  const [pick, setPick] = useState(site.main_environment);
  const [failure, setFailure] = useState<string | null>(null);
  const main = pagesDomain ? `${site.name}.${pagesDomain}` : bareAddress(site.address);
  const envs = orderedEnvironments(site.environments);
  const label = (e: string) => (pagesDomain ? `${site.name}--${e}.${pagesDomain}` : `${site.name}--${e}`);
  const access = (e: string) => (site.environments.find((x) => x.name === e)?.public ? "public" : "private");
  const after = [
    { host: main, what: pick ? `serves ${pick} (${access(pick)}).` : "answers Not found." },
    ...envs.filter((e) => e.name !== pick).map((e) => ({ host: label(e.name), what: `serves ${e.name} (${access(e.name)}).` })),
  ];
  const submit = () => {
    setFailure(null);
    move.mutate(pick, { onSuccess: onClose, onError: (e) => setFailure(errorText(e, "The main address could not be moved")) });
  };
  const option = (value: string, title: string, help: string) => (
    <label key={value || "_none"} className={`flex cursor-pointer items-start gap-2 rounded border p-3 ${pick === value ? "border-primary bg-primary/5" : ""}`}>
      <input type="radio" name="main" aria-label={title} className="mt-1" checked={pick === value} onChange={() => setPick(value)} />
      <span>
        <span className="flex flex-wrap items-center gap-2 font-medium">
          {title}
          {site.main_environment === value && <span className="rounded-full bg-muted px-2 text-xs font-medium text-muted-foreground">Now</span>}
        </span>
        <span className="block text-xs text-muted-foreground">{help}</span>
      </span>
    </label>
  );
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Main address</DialogTitle>
          <DialogDescription>
            Which environment people reach at <span className="break-all font-mono">{main}</span>
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-3">
          <fieldset className="space-y-2">
            <legend className="sr-only">Serve the main address from</legend>
            {envs.map((e) =>
              option(e.name, e.name, e.name === DEV_ENVIRONMENT ? "The working copy. Fits a site whose main address is for the people building it." : "Changes only by Promote and Roll back."),
            )}
            {option("", "None", "Every environment answers at its own address or domain only.")}
          </fieldset>
          <div className="space-y-1 rounded border p-3">
            <p className="text-sm font-medium">After saving</p>
            {after.map((a) => (
              <p key={a.host} className="text-sm">
                <span className="break-all font-mono">{a.host}</span> <span className="text-muted-foreground">{a.what}</span>
              </p>
            ))}
          </div>
          <p className="text-xs text-muted-foreground">Every address is under the pages domain, so nothing changes in DNS or the ingress. Each environment keeps its own access.</p>
          <Failure text={failure} />
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={pick === site.main_environment || move.isPending}>
            Save
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
