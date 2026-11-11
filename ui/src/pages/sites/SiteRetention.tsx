import { Lock } from "lucide-react";
import type { Site, SiteEnvironment, SiteRelease } from "@/api/portal/hooks/sites";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Input } from "@/components/ui/input";

// Releases kept (#2602): each environment keeps the last N releases it served,
// the deployment's default unless its owner sets its own, and the daily
// cleanup removes every release no environment keeps, with its files. Only the
// owner or an administrator changes a limit, because lowering it removes
// releases for good.

/** What an environment keeps, as its card says it: "Last 50 · 37 kept". */
export function keepSummary(env: SiteEnvironment): { value: string; detail: string } {
  const value = `${env.keep_releases === 0 ? "All" : `Last ${env.keep_releases}`} · ${env.releases_kept} kept`;
  let detail = `Set for ${env.name}`;
  if (env.keep_releases_default) detail = "Deployment default";
  else if (env.keep_releases === 0) detail = `Every release ${env.name} has served`;
  return { value, detail };
}

export function KeptLine({ env }: { env: SiteEnvironment }) {
  const { value, detail } = keepSummary(env);
  return (
    <div className="space-y-1" data-testid={`kept-${env.name}`}>
      <p className="text-xs font-medium text-muted-foreground">Releases kept</p>
      <p className="text-sm">{value}</p>
      <p className="text-xs text-muted-foreground">{detail}</p>
    </div>
  );
}

/** The deployment's default, read from an environment that follows it; undefined when none does. */
export function deploymentDefault(site: Site): number | undefined {
  return site.environments.find((e) => e.keep_releases_default)?.keep_releases;
}

export type KeepMode = "default" | "last" | "all";

export interface KeepChoice {
  mode: KeepMode;
  count: string;
}

export function keepChoiceOf(env: SiteEnvironment): KeepChoice {
  if (env.keep_releases_default) return { mode: "default", count: String(env.keep_releases) };
  if (env.keep_releases === 0) return { mode: "all", count: "20" };
  return { mode: "last", count: String(env.keep_releases) };
}

/** What a save sends for the choice, undefined when it changes nothing or the count is not a whole number. */
export function keepBody(env: SiteEnvironment, c: KeepChoice): number | "default" | undefined {
  const was = keepChoiceOf(env);
  if (c.mode === was.mode && (c.mode !== "last" || c.count === was.count)) return undefined;
  if (c.mode === "default") return "default";
  if (c.mode === "all") return 0;
  const n = Number(c.count);
  return Number.isInteger(n) && n > 0 ? n : undefined;
}

/** How many releases the environment keeps now would no longer be kept under the choice. */
export function dropped(site: Site, env: SiteEnvironment, c: KeepChoice): number {
  let limit: number | undefined;
  if (c.mode === "all") return 0;
  if (c.mode === "default") limit = deploymentDefault(site);
  else limit = Number(c.count);
  if (!limit || !Number.isFinite(limit) || limit <= 0) return 0;
  return Math.max(0, env.releases_kept - limit);
}

export function ReleasesKeptSection({ site, env, choice, onChange }: { site: Site; env: SiteEnvironment; choice: KeepChoice; onChange: (c: KeepChoice) => void }) {
  const own = site.permissions.own;
  const fallback = deploymentDefault(site);
  const lost = own ? dropped(site, env, choice) : 0;
  const pick = (mode: KeepMode, title: string, help: string, extra?: React.ReactNode) => (
    <label className={`flex items-start gap-2 rounded border p-3 ${choice.mode === mode ? "border-primary bg-primary/5" : ""} ${own ? "cursor-pointer" : "opacity-60"}`}>
      <input type="radio" name="keep" aria-label={title} className="mt-1" checked={choice.mode === mode} disabled={!own} onChange={() => onChange({ ...choice, mode })} />
      <span className="min-w-0">
        <span className="block font-medium">{title}</span>
        {help && <span className="block text-xs text-muted-foreground">{help}</span>}
        {extra}
      </span>
    </label>
  );
  return (
    <fieldset className="space-y-2" data-testid="releases-kept">
      <legend className="mb-2 text-sm font-medium">Releases kept</legend>
      {pick("default", "Deployment default", fallback === undefined ? "The deployment's default." : `The last ${fallback} releases ${env.name} has served.`)}
      {pick(
        "last",
        "Last releases served",
        "",
        <span className="mt-1 flex items-center gap-2 text-sm">
          <Input
            type="number"
            min={1}
            aria-label="Releases to keep"
            className="h-8 w-24"
            value={choice.count}
            disabled={!own}
            onFocus={() => onChange({ ...choice, mode: "last" })}
            onChange={(e) => onChange({ mode: "last", count: e.target.value })}
          />
          releases
        </span>,
      )}
      {pick("all", "All", `Every release ${env.name} has served. Nothing it served is removed.`)}
      <p className="text-xs text-muted-foreground">
        Roll back can return {env.name} to any release it keeps. A release no environment keeps is removed by the daily cleanup, with its files.
      </p>
      {lost > 0 && (
        <Alert variant="warning" data-testid="keep-warning">
          <AlertTitle>
            {lost} {lost === 1 ? "release" : "releases"} {env.name} served will no longer be kept
          </AlertTitle>
          <AlertDescription>At the next cleanup, those no other environment keeps are removed for good, and {env.name} can’t be rolled back to them.</AlertDescription>
        </Alert>
      )}
      {!own && (
        <p className="flex items-start gap-1.5 text-xs text-muted-foreground" data-testid="keep-locked">
          <Lock className="mt-0.5 h-3 w-3 shrink-0" aria-hidden />
          Only the site’s owner or an administrator changes this, because lowering it removes releases for good.
        </p>
      )}
    </fieldset>
  );
}

/** The kept releases a rollback can return env to, newest first. */
export function rollbackChoices(env: SiteEnvironment, served: number[], releases?: SiteRelease[]): number[] {
  const removed = new Set((releases ?? []).filter((r) => r.pruned).map((r) => r.number));
  return [...new Set(served)].filter((n) => n !== env.release && !removed.has(n)).sort((a, b) => b - a);
}

/** The sentence under a rollback's choices about releases the limit removed. */
export function rollbackNote(env: SiteEnvironment): string {
  if (env.keep_releases === 0) return `${env.name} keeps every release it has served.`;
  return `${env.name} keeps the last ${env.keep_releases} releases it served; older ones were removed and can’t be chosen.`;
}
