import { useState } from "react";
import { ArrowRight, CircleHelp } from "lucide-react";
import {
  DEV_ENVIRONMENT,
  useApprovalForSite,
  usePromoteSite,
  useSetSiteEnvironment,
  useSiteAccess,
  type Site,
  type SiteAccessList,
  type SiteEnvironment,
} from "@/api/portal/hooks/sites";
import type { ApprovalRequest } from "@/api/portal/hooks/approvals";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { useAuthStore } from "@/stores/auth";
import { EnvironmentDialogs, type EnvDialog } from "./SiteEnvironmentDialogs";
import { AccessBadge, AddressLink, EVERYONE_INTERNAL, Failure, ShareChip, grantName, grantOpens } from "./SiteParts";
import { bareAddress, envAddress, errorText, hostUrl, hostsOf, orderedEnvironments, when } from "./siteFormat";

const EXPLAINER_KEY = "sites.explainer.hidden";

function readHidden(): boolean {
  try {
    return window.localStorage.getItem(EXPLAINER_KEY) === "1";
  } catch {
    return false;
  }
}

function writeHidden(hidden: boolean) {
  try {
    if (hidden) window.localStorage.setItem(EXPLAINER_KEY, "1");
    else window.localStorage.removeItem(EXPLAINER_KEY);
  } catch {
    // A browser that keeps nothing shows the explainer again next time.
  }
}

// SiteEnvironmentsTab is each environment in the order a release is promoted
// in (#2604): agents and the people who manage the site change dev, people use
// the environments a release is promoted to, and each one says where it answers
// and who can open it.
export function SiteEnvironmentsTab({ site, onNavigate }: { site: Site; onNavigate: (path: string) => void }) {
  const [dialog, setDialog] = useState<EnvDialog>(null);
  const [hidden, setHidden] = useState(readHidden);
  const canEdit = site.permissions.manage;
  const access = useSiteAccess(site.name, canEdit).data;
  const envs = orderedEnvironments(site.environments);
  const hide = (h: boolean) => {
    writeHidden(h);
    setHidden(h);
  };
  return (
    <>
      <ApprovalBanners site={site} onNavigate={onNavigate} />
      <Guide site={site} canEdit={canEdit} hidden={hidden} onHide={hide} />
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h3 className="text-sm font-semibold">Environments</h3>
        {canEdit && <HeaderActions site={site} onAct={setDialog} />}
      </div>
      {envs.length === 0 && (
        <div className="rounded border border-dashed p-6 text-center">
          <p className="font-medium">No environment to show</p>
          <p className="text-xs text-muted-foreground">None of {site.name}'s environments is open to you. Ask the person who sent you the link to share one.</p>
        </div>
      )}
      <div className="flex flex-col gap-2 lg:flex-row lg:items-stretch" data-testid="environment-flow">
        {envs.map((e, i) => (
          <div key={e.name} className="flex min-w-0 flex-1 flex-col gap-2 lg:flex-row lg:items-stretch">
            {i > 0 && (
              <div className="flex items-center justify-center text-muted-foreground" aria-hidden>
                <ArrowRight className="h-5 w-5 rotate-90 lg:rotate-0" />
              </div>
            )}
            <EnvironmentCard site={site} env={e} access={access} canEdit={canEdit} onAct={setDialog} />
          </div>
        ))}
      </div>
      <EnvironmentDialogs site={site} dialog={dialog} onClose={() => setDialog(null)} onNavigate={onNavigate} />
    </>
  );
}

function Guide({ site, canEdit, hidden, onHide }: { site: Site; canEdit: boolean; hidden: boolean; onHide: (h: boolean) => void }) {
  if (!canEdit) {
    return <p className="text-sm text-muted-foreground">These are the environments of {site.name} shared with you. Open one at its address; you sign in with your platform account.</p>;
  }
  if (hidden) {
    return (
      <button type="button" className="inline-flex w-fit items-center gap-1 text-sm text-primary hover:underline" onClick={() => onHide(false)}>
        <CircleHelp className="h-4 w-4" aria-hidden />
        How this site works
      </button>
    );
  }
  return <Explainer site={site} onHide={() => onHide(true)} />;
}

function HeaderActions({ site, onAct }: { site: Site; onAct: (d: EnvDialog) => void }) {
  return (
    <div className="flex flex-wrap gap-2">
      {site.permissions.own && (
        <Button size="sm" variant="outline" onClick={() => onAct({ kind: "main" })}>
          Main address
        </Button>
      )}
      <Button size="sm" variant="outline" onClick={() => onAct({ kind: "domain" })}>
        Add domain
      </Button>
      <Button size="sm" variant="outline" onClick={() => onAct({ kind: "new" })}>
        New environment
      </Button>
    </div>
  );
}

const STEPS: [string, string][] = [
  ["Changes land in dev", "Publishing, editing files and agents change dev and nothing else. dev is the working copy: only the people who manage this site can open it, unless you share it."],
  ["Promote what is ready", "Promote serves the exact release from dev in another environment. Those environments change only by Promote and Roll back, so nobody edits what people are using."],
  [
    "Each environment has its own access",
    "Private asks for a sign-in to this platform and opens only for the people and personas it is shared with. Public opens for anyone; a site that wants a sign-in builds its own.",
  ],
];

/** The pages domain an environment's address is under, read from one of them. */
function pagesDomainOf(site: Site): string {
  const any = bareAddress(Object.values(site.addresses ?? {}).find((a) => a.includes(`${site.name}--`)) ?? "");
  const dot = any.indexOf(".");
  return dot > 0 ? any.slice(dot + 1) : "";
}

function Explainer({ site, onHide }: { site: Site; onHide: () => void }) {
  const main = site.address ? bareAddress(site.address) : "";
  const domain = pagesDomainOf(site);
  return (
    <SectionCard
      title="How this site works"
      action={
        <Button size="sm" variant="ghost" onClick={onHide}>
          Hide
        </Button>
      }
    >
      <div className="space-y-4">
        <ol className="grid gap-4 md:grid-cols-3">
          {STEPS.map(([title, body], i) => (
            <li key={title} className="flex items-start gap-3">
              <span className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-primary/10 text-sm font-semibold text-primary" aria-hidden>
                {i + 1}
              </span>
              <div className="space-y-1">
                <p className="font-medium">{title}</p>
                <p className="text-sm text-muted-foreground">{body}</p>
              </div>
            </li>
          ))}
        </ol>
        <div className="grid gap-3 border-t pt-3 md:grid-cols-2">
          <p className="text-xs text-muted-foreground">
            <span className="font-medium text-foreground">Addresses. </span>
            {main ? (
              <>
                The main address, <span className="break-all font-mono">{main}</span>, serves the one environment you choose.{" "}
              </>
            ) : (
              <>No environment holds the main address, so it answers Not found. </>
            )}
            {domain && (
              <>
                Every environment also answers at <span className="break-all font-mono">{site.name}--</span>
                <span className="font-mono italic">environment</span>
                <span className="break-all font-mono">.{domain}</span>, and at any domain added to it.
              </>
            )}
          </p>
          <p className="text-xs text-muted-foreground">
            <span className="font-medium text-foreground">Inside a private environment. </span>
            The site can ask who signed in and run its endpoints as that person, so it can decide for itself which pages each of them sees.
          </p>
        </div>
      </div>
    </SectionCard>
  );
}

function EnvironmentCard({
  site,
  env,
  access,
  canEdit,
  onAct,
}: {
  site: Site;
  env: SiteEnvironment;
  access?: SiteAccessList;
  canEdit: boolean;
  onAct: (d: EnvDialog) => void;
}) {
  const working = env.name === DEV_ENVIRONMENT;
  return (
    <section className={`flex min-w-0 flex-1 flex-col gap-3 rounded-xl border bg-card p-4 shadow-sm ${working ? "border-primary/45" : ""}`} aria-label={env.name} data-testid={`env-${env.name}`}>
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div>
          <p className="text-base font-semibold">{env.name}</p>
          <p className="text-xs text-muted-foreground">{working ? "Working copy" : "Changes by Promote and Roll back"}</p>
        </div>
        <div className="flex flex-wrap justify-end gap-2">
          <AccessBadge env={env} />
          {env.requires_approval && <Badge variant="warning">Requires approval</Badge>}
        </div>
      </div>
      <Release env={env} />
      <Addresses site={site} env={env} />
      <WhoCanOpen site={site} env={env} access={access} />
      {canEdit && <CardActions env={env} onAct={onAct} />}
    </section>
  );
}

function Release({ env }: { env: SiteEnvironment }) {
  return (
    <div className="space-y-1">
      <p className="text-xs font-medium text-muted-foreground">Release</p>
      {env.release > 0 ? (
        <>
          <Badge variant="info">{env.release}</Badge>
          <p className="text-xs text-muted-foreground">
            Moved {when(env.updated_at)}
            {env.updated_by ? ` by ${env.updated_by}` : ""}
          </p>
        </>
      ) : (
        <p className="text-sm text-muted-foreground">None yet</p>
      )}
    </div>
  );
}

function Addresses({ site, env }: { site: Site; env: SiteEnvironment }) {
  const own = envAddress(site, env.name);
  const main = site.main_environment === env.name;
  const domains = hostsOf(site, env.name);
  return (
    <div className="space-y-1">
      <p className="text-xs font-medium text-muted-foreground">Addresses</p>
      {own && (
        <div className="flex flex-wrap items-center gap-1.5">
          <AddressLink href={own} text={bareAddress(own)} />
          {main && <Badge variant="muted">Main address</Badge>}
        </div>
      )}
      {domains.map((d) => (
        <div key={d.hostname} className="flex flex-wrap items-center gap-1.5">
          <AddressLink href={hostUrl(site, d.hostname)} text={d.hostname} />
          <Badge variant="muted">Domain</Badge>
        </div>
      ))}
      {!own && domains.length === 0 && <p className="text-sm text-muted-foreground">No address</p>}
    </div>
  );
}

function managersSentence(n: number): string {
  if (n === 0) return "The owner and administrators.";
  if (n === 1) return "The owner, the person who manages this site and administrators.";
  return `The owner, the ${n} people who manage this site and administrators.`;
}

// WhoCanOpen says, in words, who an environment opens for, and names the people
// and personas it is shared with.
function WhoCanOpen({ site, env, access }: { site: Site; env: SiteEnvironment; access?: SiteAccessList }) {
  if (env.public) {
    return (
      <div className="space-y-1">
        <p className="text-xs font-medium text-muted-foreground">Who can open it</p>
        <p className="text-sm">Anyone. The platform asks no one to sign in.</p>
      </div>
    );
  }
  return <PrivateOpeners site={site} env={env} access={access} />;
}

function PrivateOpeners({ site, env, access }: { site: Site; env: SiteEnvironment; access?: SiteAccessList }) {
  const grants = access?.grants ?? [];
  const shared = grants.filter((g) => grantOpens(g, env.name));
  const any = shared.length > 0 || env.internal_view;
  return (
    <div className="space-y-1">
      <p className="text-xs font-medium text-muted-foreground">Who can open it</p>
      <p className="text-sm">{any ? "The people who manage this site, administrators, and:" : managersSentence(grants.filter((g) => g.level === "manage").length)}</p>
      {any && (
        <div className="flex flex-wrap gap-1.5">
          {env.internal_view && <ShareChip label={EVERYONE_INTERNAL} kind="internal" />}
          {shared.map((g) => (
            <ShareChip key={grantName(g)} label={grantName(g)} kind={g.persona ? "persona" : "person"} />
          ))}
        </div>
      )}
      {!any && access && site.permissions.manage && <p className="text-xs text-muted-foreground">Not shared with anyone else.</p>}
    </div>
  );
}

function CardActions({ env, onAct }: { env: SiteEnvironment; onAct: (d: EnvDialog) => void }) {
  const working = env.name === DEV_ENVIRONMENT;
  return (
    <div className="mt-auto flex flex-wrap gap-2 border-t pt-3">
      {working ? (
        <Button size="sm" onClick={() => onAct({ kind: "publish" })}>
          Publish
        </Button>
      ) : (
        <Button size="sm" variant="outline" onClick={() => onAct({ kind: "promote", env })}>
          Promote
        </Button>
      )}
      <Button size="sm" variant="outline" disabled={env.release === 0} onClick={() => onAct({ kind: "rollback", env })}>
        Roll back
      </Button>
      {!env.public && (
        <Button size="sm" variant="outline" onClick={() => onAct({ kind: "share", env })}>
          Share
        </Button>
      )}
      <Button size="sm" variant="outline" onClick={() => onAct({ kind: "settings", env })}>
        Settings
      </Button>
    </div>
  );
}

// ApprovalBanners says where each request that needs approval stands: a
// promote, or making an environment public.
function ApprovalBanners({ site, onNavigate }: { site: Site; onNavigate: (path: string) => void }) {
  const requests = useApprovalForSite(site);
  const [failure, setFailure] = useState<string | null>(null);
  return (
    <>
      {requests.map((r) => (
        <ApprovalBanner key={r.id} site={site} request={r} onNavigate={onNavigate} onFail={setFailure} />
      ))}
      <Failure text={failure} />
    </>
  );
}

type SiteApproval = ApprovalRequest & { target: string };

function ApprovalBanner({ site, request: r, onNavigate, onFail }: { site: Site; request: SiteApproval; onNavigate: (path: string) => void; onFail: (t: string) => void }) {
  const email = useAuthStore((s) => s.user?.email ?? "");
  const mine = r.requester_email === email || site.permissions.manage;
  const publicReq = r.purpose === "site_public";
  if (r.status === "pending") return <Waiting request={r} publicReq={publicReq} onNavigate={onNavigate} />;
  if (r.status === "approved" && mine) {
    return publicReq ? <PublicApproved site={site} request={r} onFail={onFail} /> : <PromoteApproved site={site} request={r} onFail={onFail} />;
  }
  if (r.status !== "denied") return null;
  return <Rejected request={r} publicReq={publicReq} />;
}

function Rejected({ request: r, publicReq }: { request: SiteApproval; publicReq: boolean }) {
  return (
    <Alert variant="destructive" data-testid={publicReq ? "public-rejected" : "approval-rejected"}>
      <AlertTitle>{publicReq ? `Making ${r.target} public was rejected` : `Release ${r.version} was rejected for ${r.target}`}</AlertTitle>
      <AlertDescription>
        Rejected by {r.decided_by} on {when(r.decided_at ?? "")}
        {r.decision_comment ? `: "${r.decision_comment}"` : "."}
      </AlertDescription>
    </Alert>
  );
}

function Waiting({ request: r, publicReq, onNavigate }: { request: SiteApproval; publicReq: boolean; onNavigate: (path: string) => void }) {
  return (
    <Alert data-testid={publicReq ? "public-waiting" : "approval-waiting"}>
      <AlertTitle>{publicReq ? `Making ${r.target} public is waiting for approval` : `Release ${r.version} is waiting for approval to serve ${r.target}`}</AlertTitle>
      <AlertDescription className="flex flex-wrap items-center justify-between gap-2">
        <span>
          Asked by {r.requester_email} on {when(r.created_at)}.{" "}
          {publicReq ? `${r.target} stays private until it is approved and you finish.` : `${r.target} keeps serving its release until this is approved and promoted.`}
        </span>
        <Button size="sm" variant="outline" onClick={() => onNavigate("/approvals")}>
          Open in Approvals
        </Button>
      </AlertDescription>
    </Alert>
  );
}

function PromoteApproved({ site, request: r, onFail }: { site: Site; request: SiteApproval; onFail: (t: string) => void }) {
  const promote = usePromoteSite(site.name);
  const finish = () => promote.mutate({ to: r.target, release: r.version }, { onError: (e) => onFail(errorText(e, "The promote could not be finished")) });
  return (
    <Alert data-testid="approval-approved">
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

function PublicApproved({ site, request: r, onFail }: { site: Site; request: SiteApproval; onFail: (t: string) => void }) {
  const set = useSetSiteEnvironment(site.name);
  const finish = () => set.mutate({ name: r.target, public: true }, { onError: (e) => onFail(errorText(e, "The environment could not be made public")) });
  return (
    <Alert data-testid="public-approved">
      <AlertTitle>Making {r.target} public is approved</AlertTitle>
      <AlertDescription className="flex flex-wrap items-center justify-between gap-2">
        <span>
          Approved by {r.decided_by} on {when(r.decided_at ?? "")}. Nothing changes on {r.target} until you finish.
        </span>
        <Button size="sm" onClick={finish} disabled={set.isPending}>
          Make {r.target} public
        </Button>
      </AlertDescription>
    </Alert>
  );
}
