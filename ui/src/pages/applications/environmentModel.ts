import type { ApplicationEnvironmentSchema, ApplicationPrepareCounts } from "@/api/portal/hooks/applicationEnvironments";
import type { ApplicationDetail, ApplicationRelease, ApplicationSitePart } from "@/api/portal/hooks/applications";

// What the Environments and Site parts cards show of each environment
// (#2709): the schema it runs in, the site environments that call it (a site
// environment calls the application environment of its own name), and what a
// promote into it does. Read from the application's page alone.

/** siteEnvOf splits a site part's id, "<site>/<environment>". */
export function siteEnvOf(partId: string): { site: string; env: string } {
  const at = partId.indexOf("/");
  return at < 0 ? { site: partId, env: "" } : { site: partId.slice(0, at), env: partId.slice(at + 1) };
}

/** siteLabel is a site part as the page writes it: "shop-web / dev". */
export function siteLabel(partId: string): string {
  const { site, env } = siteEnvOf(partId);
  return env ? `${site} / ${env}` : site;
}

/** envSchemaOf is the schema env runs in, as the schema view reports it. */
export function envSchemaOf(d: ApplicationDetail, env: string): ApplicationEnvironmentSchema | undefined {
  const listed = d.schema?.environments?.find((e) => e.environment === env);
  if (listed) return listed;
  const e = d.application.environments?.find((x) => x.name === env);
  if (!d.application.schema || !e) return undefined;
  return { environment: env, schema: e.schema || d.application.schema, migration_version: 0, pending: [] };
}

/** sitePartsCalling is the site parts whose environment is env. */
export function sitePartsCalling(d: ApplicationDetail, env: string): ApplicationSitePart[] {
  return (d.site_parts ?? []).filter((p) => siteEnvOf(p.part_id).env === env);
}

/** hasEnvironment reports whether the application has an environment named env. */
export function hasEnvironment(d: ApplicationDetail, env: string): boolean {
  return (d.application.environments ?? []).some((e) => e.name === env);
}

/** defaultSchema is the schema an environment added with none named runs in. */
export function defaultSchema(appSchema: string, env: string): string {
  return `${appSchema}_${env.replace(/-/g, "_")}`;
}

/** prepareTotal is every file that names the schema or its roles. */
export function prepareTotal(c?: ApplicationPrepareCounts): number {
  return c ? c.scripts + c.libraries + c.migrations : 0;
}

/** counted is "1 script" or "3 scripts". */
export function counted(n: number, noun: string, plural = `${noun}s`): string {
  return `${n} ${n === 1 ? noun : plural}`;
}

/** What a promote of one environment's release into the next does. */
export interface PromotePlan {
  release: number;
  replaced: number;
  /** Scripts whose pinned version differs, null when the releases are not on the page. */
  scripts: number | null;
  /** The migrations applied to the target's schema, and that schema. */
  migrations: number[];
  schema: string;
  /** The coupled site parts calling the target, and the release each moves to. */
  sites: { part_id: string; to: number | null }[];
}

function releaseOf(d: ApplicationDetail, n: number): ApplicationRelease | undefined {
  return d.releases.find((r) => r.number === n);
}

/** changedScripts is how many scripts promoted pins at another version than running. */
function changedScripts(promoted: ApplicationRelease | undefined, running: ApplicationRelease | undefined): number | null {
  if (!promoted) return null;
  const was = new Map((running?.pins ?? []).filter((p) => p.kind === "script").map((p) => [p.part_id, p.version]));
  return promoted.pins.filter((p) => p.kind === "script" && was.get(p.part_id) !== p.version).length;
}

/** sitesMoving is the coupled site parts calling to and the release promoted pins each at. */
function sitesMoving(d: ApplicationDetail, to: string, promoted: ApplicationRelease | undefined): PromotePlan["sites"] {
  const pinOf = (id: string) => promoted?.pins.find((x) => x.kind === "site" && x.part_id === id)?.version ?? null;
  return sitePartsCalling(d, to)
    .filter((p) => p.release_mode === "coupled")
    .map((p) => ({ part_id: p.part_id, to: pinOf(p.part_id) }));
}

/** envRelease is the release env runs, 0 for none or no such environment. */
function envRelease(d: ApplicationDetail, env: string): number {
  return d.application.environments?.find((e) => e.name === env)?.release ?? 0;
}

/** lastEnvName is the application's last environment, the one /endpoints serves. */
export function lastEnvName(d: ApplicationDetail): string {
  const envs = d.application.environments ?? [];
  return envs[envs.length - 1]?.name ?? "prod";
}

/** applied is the migrations target's schema has pending through the promoted release's version. */
function applied(target: ApplicationEnvironmentSchema | undefined, promoted: ApplicationRelease | undefined): number[] {
  if (!target || !promoted) return [];
  return target.pending.filter((v) => v <= promoted.migration_version);
}

/** promotePlan is what promoting from's release into to does. */
export function promotePlan(d: ApplicationDetail, from: string, to: string): PromotePlan {
  const release = envRelease(d, from);
  const replaced = envRelease(d, to);
  const promoted = releaseOf(d, release);
  const target = envSchemaOf(d, to);
  return {
    release,
    replaced,
    scripts: changedScripts(promoted, releaseOf(d, replaced)),
    migrations: applied(target, promoted),
    schema: target?.schema ?? "",
    sites: sitesMoving(d, to, promoted),
  };
}

/** promoteItems is the list the Promote dialog shows of plan. */
export function promoteItems(plan: PromotePlan, to: string): string[] {
  const items: string[] = [];
  if (plan.scripts !== null) {
    items.push(plan.scripts === 0 ? `runs no changed script in ${to}` : `runs ${counted(plan.scripts, "changed script")} in ${to}`);
  }
  if (plan.schema) {
    const n = plan.migrations.length;
    items.push(n === 0 ? `applies no migration to ${plan.schema}` : `applies ${n === 1 ? "migration" : "migrations"} ${plan.migrations.join(", ")} to ${plan.schema}`);
  }
  for (const s of plan.sites) {
    items.push(`moves ${siteLabel(s.part_id)} (coupled)${s.to ? ` to release ${s.to}` : ""}`);
  }
  return items;
}
