import { useSetDomainRole, type DomainMode, type Site, type SiteDomain } from "@/api/portal/hooks/sites";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { errorText } from "./siteFormat";

// A domain's role among its environment's domains (#2687): the primary is the
// canonical hostname, and a domain set to redirect answers every request with
// a permanent redirect to it.

/** The environment's primary domain, if it has one. */
export function primaryOf(site: Site, environment: string): SiteDomain | undefined {
  return site.domains.find((d) => d.environment === environment && d.primary);
}

/** RoleCell is what the Role column shows for one domain. */
export function RoleCell({ domain: d }: { domain: SiteDomain }) {
  if (d.primary) return <Badge variant="info">Primary</Badge>;
  if (d.mode === "redirect") {
    return (
      <div className="flex flex-col gap-0.5">
        <span>
          <Badge variant="muted">Redirect</Badge>
        </span>
        {d.redirects_to && <span className="text-xs text-muted-foreground">301 to {d.redirects_to}</span>}
      </div>
    );
  }
  return <span className="text-muted-foreground">Serves</span>;
}

/** RoleButtons are a domain's role actions, each refused change reported through onError. */
export function RoleButtons({ site, domain: d, onError }: { site: Site; domain: SiteDomain; onError: (msg: string | null) => void }) {
  const set = useSetDomainRole(site.name);
  const hasPrimary = !!primaryOf(site, d.environment ?? "");
  const change = (body: { primary?: boolean; mode?: DomainMode }) => {
    onError(null);
    set.mutate({ hostname: d.hostname, ...body }, { onError: (e) => onError(errorText(e, "This domain's role could not be changed")) });
  };
  return (
    <>
      {!d.primary && (
        <Button size="sm" variant="outline" disabled={set.isPending} onClick={() => change({ primary: true })}>
          Make primary
        </Button>
      )}
      {!d.primary && d.mode !== "redirect" && hasPrimary && (
        <Button size="sm" variant="outline" disabled={set.isPending} onClick={() => change({ mode: "redirect" })}>
          Redirect to primary
        </Button>
      )}
      {d.mode === "redirect" && (
        <Button size="sm" variant="outline" disabled={set.isPending} onClick={() => change({ mode: "serve" })}>
          Serve here
        </Button>
      )}
    </>
  );
}

export type AddRole = "serve" | "primary" | "redirect";

/** The body fields an Add domain role sends. */
export function roleBody(role: AddRole): { primary?: boolean; mode?: DomainMode } {
  if (role === "primary") return { primary: true };
  if (role === "redirect") return { mode: "redirect" };
  return {};
}

/** DomainRoleChoice is the Role choice of Add domain. */
export function DomainRoleChoice({ site, environment, role, onRole }: { site: Site; environment: string; role: AddRole; onRole: (r: AddRole) => void }) {
  const env = environment || "the environment";
  const primary = environment ? primaryOf(site, environment) : undefined;
  const option = (value: AddRole, title: string, help: string, disabled = false) => (
    <label className={`flex items-start gap-2 rounded border p-3 ${role === value ? "border-primary bg-primary/5" : ""} ${disabled ? "opacity-60" : "cursor-pointer"}`}>
      <input type="radio" name="domain-role" aria-label={title} className="mt-1" checked={role === value} disabled={disabled} onChange={() => onRole(value)} />
      <span>
        <span className="block font-medium">{title}</span>
        <span className="block text-xs text-muted-foreground">{help}</span>
      </span>
    </label>
  );
  return (
    <fieldset className="space-y-2">
      <legend className="mb-2 text-sm font-medium">Role</legend>
      {option("serve", "Serves", `Serves ${env}'s release here.`)}
      {option(
        "primary",
        "Primary",
        `${env}'s canonical domain. Serves the release; the domains set to redirect send visitors here.${primary ? ` Takes the place of ${primary.hostname}, which keeps serving.` : ""}`,
      )}
      {primary
        ? option("redirect", `Redirect to ${primary.hostname}`, `Answers every request with a permanent redirect to ${primary.hostname}, path and query kept.`)
        : option("redirect", "Redirect to the primary", `${env} has no primary domain yet.`, true)}
    </fieldset>
  );
}
