import { useState } from "react";
import {
  DEV_ENVIRONMENT,
  answersAnyone,
  placeChoices,
  useSetMainEnvironment,
  useSetSiteEnvironment,
  useSites,
  type Site,
  type SiteEnvironment,
} from "@/api/portal/hooks/sites";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { AddDomainDialog } from "./SiteDomains";
import { envNameProblem } from "./envName";
import { Failure } from "./SiteParts";
import { PromoteDialog, RollbackDialog } from "./SitePromote";
import { ReleasesKeptSection, keepBody, keepChoiceOf, type KeepChoice } from "./SiteRetention";
import { PublishDialog } from "./SitePublish";
import { JobDialog } from "./SitePublishJob";
import { ShareDialog } from "./SiteShare";
import { bareAddress, errorText, orderedEnvironments } from "./siteFormat";

export type EnvDialog =
  | { kind: "promote" | "rollback" | "share" | "settings" | "move"; env: SiteEnvironment }
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
function EnvironmentDialog({ site, dialog, onClose }: { site: Site; dialog: { kind: "promote" | "rollback" | "share" | "settings" | "move"; env: SiteEnvironment }; onClose: () => void }) {
  switch (dialog.kind) {
    case "move":
      return <MoveDialog site={site} env={dialog.env} onClose={onClose} />;
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
  const order = site.environments.map((e) => e.name);
  const [name, setName] = useState("");
  const [approval, setApproval] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const trimmed = name.trim();
  const choices = placeChoices(order, trimmed);
  const [after, setAfter] = useState(choices[0] ?? DEV_ENVIRONMENT);
  const problem = newNameProblem(site, trimmed);
  const submit = () => {
    setFailure(null);
    set.mutate(
      { name: trimmed, requires_approval: approval, ...(choices.length > 1 ? { after } : {}) },
      { onSuccess: onClose, onError: (e) => setFailure(errorText(e, "The environment could not be created")) },
    );
  };
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-h-[90dvh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>New environment</DialogTitle>
          <DialogDescription>{site.name}</DialogDescription>
        </DialogHeader>
        <div className="space-y-3">
          <div className="space-y-1">
            <Label htmlFor="env-name">Name</Label>
            <Input id="env-name" value={name} onChange={(e) => setName(e.target.value)} aria-invalid={!!problem} autoFocus />
            <p className="text-xs text-muted-foreground">Lowercase letters, digits and single hyphens, up to 32. It is part of the address, so it does not change.</p>
          </div>
          <NewAddress site={site.name} name={trimmed} pagesDomain={pagesDomain} problem={problem} />
          <GoesAfter order={order} name={trimmed || "new"} choices={choices} after={after} onAfter={setAfter} />
          <ApprovalCheck checked={approval} onChange={setApproval} help={APPROVAL_HELP} />
          <p className="text-xs text-muted-foreground">It starts with no release. Promote the release of the environment before it when it is ready.</p>
          <Failure text={failure} />
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={!trimmed || !!problem || set.isPending}>
            Create environment
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/** Why a new environment's name cannot be used, "" for none. */
function newNameProblem(site: Site, name: string): string {
  if (site.environments.some((e) => e.name === name)) return `${site.name} already has an environment named ${name}.`;
  return envNameProblem(site.name, name);
}

/** The order with name put after another, as a row of names. */
function placedOrder(order: string[], name: string, after: string): string[] {
  const rest = order.filter((n) => n !== name);
  const at = rest.indexOf(after);
  return at < 0 ? [...rest, name] : [...rest.slice(0, at + 1), name, ...rest.slice(at + 1)];
}

function Flow({ names, name }: { names: string[]; name: string }) {
  return (
    <span className="flex flex-wrap items-center gap-1.5 text-xs text-muted-foreground" data-testid="env-flow">
      {names.map((n, i) => (
        <span key={n} className="flex items-center gap-1.5">
          {i > 0 && <span aria-hidden>→</span>}
          <span className={`rounded-full border px-2 ${n === name ? "border-primary font-semibold text-primary" : "text-foreground"}`}>{n}</span>
        </span>
      ))}
    </span>
  );
}

// GoesAfter is where an environment goes in the site's order (#2617): after
// dev or another environment, never after prod. With one place there is
// nothing to choose, and the row says where it lands.
function GoesAfter({ order, name, choices, after, onAfter, now }: { order: string[]; name: string; choices: string[]; after: string; onAfter: (v: string) => void; now?: string }) {
  if (choices.length <= 1) {
    const only = choices[0] ?? DEV_ENVIRONMENT;
    return (
      <div className="space-y-1">
        <p className="text-sm font-medium">Goes after</p>
        <Flow names={placedOrder(order, name, only)} name={name} />
        <p className="text-xs text-muted-foreground">{only}, the only place there is: nothing goes after prod.</p>
      </div>
    );
  }
  return (
    <fieldset className="space-y-2">
      <legend className="mb-2 text-sm font-medium">Goes after</legend>
      {choices.map((c) => (
        <label key={c} className={`flex cursor-pointer items-start gap-2 rounded border p-3 ${after === c ? "border-primary bg-primary/5" : ""}`}>
          <input type="radio" name="goes-after" aria-label={`After ${c}`} className="mt-1" checked={after === c} onChange={() => onAfter(c)} />
          <span className="flex flex-col gap-1">
            <span className="flex items-center gap-2 font-medium">
              After {c}
              {now === c && <span className="rounded-full bg-muted px-2 text-xs font-medium text-muted-foreground">Now</span>}
            </span>
            <Flow names={placedOrder(order, name, c)} name={name} />
          </span>
        </label>
      ))}
      <p className="text-xs text-muted-foreground">A release moves one environment at a time, left to right. dev is always first and prod always last, so nothing goes after prod.</p>
    </fieldset>
  );
}

// MoveDialog moves an environment between dev and prod (#2617). No release
// moves: each environment keeps serving what it serves.
export function MoveDialog({ site, env, onClose }: { site: Site; env: SiteEnvironment; onClose: () => void }) {
  const set = useSetSiteEnvironment(site.name);
  const order = site.environments.map((e) => e.name);
  const choices = placeChoices(order, env.name);
  const now = order[order.indexOf(env.name) - 1] ?? DEV_ENVIRONMENT;
  const [after, setAfter] = useState(choices.find((c) => c !== now) ?? now);
  const [failure, setFailure] = useState<string | null>(null);
  const next = placedOrder(order, env.name, after);
  const submit = () => {
    setFailure(null);
    set.mutate({ name: env.name, after }, { onSuccess: onClose, onError: (e) => setFailure(errorText(e, "The environment could not be moved")) });
  };
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Move {env.name}</DialogTitle>
          <DialogDescription>{site.name}</DialogDescription>
        </DialogHeader>
        <div className="space-y-3">
          <GoesAfter order={order} name={env.name} choices={choices} after={after} onAfter={setAfter} now={now} />
          <div className="space-y-1 rounded border p-3">
            <p className="text-sm font-medium">After saving</p>
            <p className="text-sm" data-testid="move-after">
              {next
                .slice(1)
                .map((n, i) => `${n} takes releases from ${next[i]}`)
                .join(", ")}
              .
            </p>
            <p className="text-xs text-muted-foreground">No release moves. Each environment keeps serving what it serves now.</p>
          </div>
          <Failure text={failure} />
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={after === now || set.isPending}>
            Move {env.name}
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

// EnvironmentSettingsDialog changes an environment's settings. Public is not
// one of them (#2619): it is a setting of a domain, on the Domains tab.
export function EnvironmentSettingsDialog({ site, env, onClose }: { site: Site; env: SiteEnvironment; onClose: () => void }) {
  const set = useSetSiteEnvironment(site.name);
  const [internal, setInternal] = useState(env.internal_view);
  const [approval, setApproval] = useState(env.requires_approval);
  const [keep, setKeep] = useState<KeepChoice>(() => keepChoiceOf(env));
  const [failure, setFailure] = useState<string | null>(null);
  const working = env.name === DEV_ENVIRONMENT;
  const keepReleases = site.permissions.own ? keepBody(env, keep) : undefined;
  const submit = () => {
    setFailure(null);
    const body = {
      name: env.name,
      internal_view: internal,
      ...(working ? {} : { requires_approval: approval }),
      ...(keepReleases !== undefined ? { keep_releases: keepReleases } : {}),
    };
    set.mutate(body, { onSuccess: onClose, onError: (e) => setFailure(errorText(e, "The settings could not be saved")) });
  };
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-h-[90dvh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{env.name} settings</DialogTitle>
          <DialogDescription>{site.name}</DialogDescription>
        </DialogHeader>
        <div className="space-y-3">
          <SettingNote
            title="Access"
            text={
              answersAnyone(env)
                ? `${env.public_domains.join(", ")} ${env.public_domains.length === 1 ? "answers" : "answer"} anyone. Every other address of ${env.name} asks for a sign-in. Change a domain's access on the Domains tab.`
                : `Every address of ${env.name} asks for a sign-in and opens for whoever it is shared with. To open it to anyone, add a domain and make that domain public on the Domains tab.`
            }
          />
          <label className="flex items-start gap-2">
            <input type="checkbox" className="mt-1" checked={internal} onChange={(e) => setInternal(e.target.checked)} />
            <span>
              <span className="block font-medium">Everyone internal can open</span>
              <span className="block text-xs text-muted-foreground">Anyone signed in to this platform whose persona is not external, as well as whoever {env.name} is shared with.</span>
            </span>
          </label>
          {working ? (
            <>
              <SettingNote title="Requires approval" text="Publishing and editing land in dev directly, so dev never requires approval." />
              <SettingNote title="Domains" text="dev can't have a domain. It is the working copy, not an address people rely on." />
            </>
          ) : (
            <ApprovalCheck checked={approval} onChange={setApproval} help={`${APPROVAL_HELP} Making one of its domains public needs the same approval.`} />
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
  const after = [
    { host: main, what: pick ? `serves ${pick}.` : "answers Not found." },
    ...envs.filter((e) => e.name !== pick).map((e) => ({ host: label(e.name), what: `serves ${e.name}.` })),
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
          <p className="text-xs text-muted-foreground">Every address is under the pages domain, so nothing changes in DNS or the ingress. Each of them asks for a sign-in.</p>
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
