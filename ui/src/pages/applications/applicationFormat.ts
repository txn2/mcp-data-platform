import type {
  ApplicationPartKind,
  ApplicationPartView,
  ApplicationReleaseAction,
  ApplicationSchemaView,
} from "@/api/portal/hooks/applications";
import { formatRelative } from "@/components/feedback/meta";
import { familyLabel } from "@/components/renderers/registry";
import { scheduleLine } from "@/pages/scripts/cadence";

// The words the Applications pages show for the values the API carries. A
// script is called an automation in the portal (#1912), so a script part is
// named the way the Automations section names it.

export const PART_KIND_LABEL: Record<ApplicationPartKind, string> = {
  script: "Automation",
  asset: "Asset",
  source: "Webhook source",
  schema: "Schema",
  site: "Site",
};

export const PART_KINDS: ApplicationPartKind[] = ["script", "asset", "source", "schema", "site"];

/** The heading of each kind's group on the Parts card and its filter pill. */
export const PART_GROUP_LABEL: Record<ApplicationPartKind, string> = {
  script: "Automations",
  asset: "Assets",
  source: "Webhook sources",
  schema: "Schema",
  site: "Sites",
};

/** partName is what a part is called on the page, never its id. */
export function partName(p: Pick<ApplicationPartView, "kind" | "state" | "name">): string {
  if (p.state === "ok" && p.name) return p.name;
  const noun = PART_KIND_LABEL[p.kind].toLowerCase();
  return p.state === "not_visible" ? `${PART_KIND_LABEL[p.kind]} you can't open` : `Deleted ${noun}`;
}

/**
 * What a schema part's line is read from (#2539): the application's schema
 * view and its current release, 0 before the first.
 */
export interface SchemaContext {
  schema?: ApplicationSchemaView;
  release: number;
}

/** partDescription is the line under a part's name: what it is and how it runs. */
export function partDescription(p: ApplicationPartView, ctx?: SchemaContext): string {
  if (p.state !== "ok") return "";
  return piecesOf(p, ctx).join(" · ");
}

function piecesOf(p: ApplicationPartView, ctx?: SchemaContext): string[] {
  switch (p.kind) {
    case "script":
      return scriptPieces(p);
    case "asset":
      return assetPieces(p);
    case "source":
      return sourcePieces(p);
    case "schema":
      return ctx?.schema ? schemaPieces(ctx.schema, ctx.release) : [];
    case "site":
      return sitePieces(p);
  }
}

function sitePieces(p: ApplicationPartView): string[] {
  const environment = `Site environment ${p.id.split("/")[1] ?? ""}`;
  return p.latest_version ? [environment, `release ${p.latest_version}`] : [environment];
}

function schemaPieces(v: ApplicationSchemaView, release: number): string[] {
  const pieces: string[] = [];
  if (!v.problem) {
    pieces.push(v.migration_version > 0 ? `migration ${v.migration_version} applied` : "no migrations applied");
  }
  if (release > 0) pieces.push(`release v${release} recorded ${v.released_version}`);
  if (v.pending.length > 0) pieces.push(`${v.pending.length} pending`);
  if (v.run_role) pieces.push(`scripts run as ${v.run_role}`);
  return pieces;
}

function scriptPieces(p: ApplicationPartView): string[] {
  const pieces: string[] = [];
  if (p.description) pieces.push(p.description);
  for (const source of p.event_sources ?? []) pieces.push(`runs on each ${source} event`);
  for (const sc of p.schedules ?? []) {
    pieces.push(sc.enabled ? lowerFirst(scheduleLine(sc.cron_spec, sc.timezone)) : "schedule paused");
  }
  return pieces;
}

function assetPieces(p: ApplicationPartView): string[] {
  const pieces: string[] = [];
  if (p.content_type) pieces.push(familyLabel(p.content_type));
  if (p.description) pieces.push(p.description);
  else if (p.updated_at) pieces.push(`updated ${formatRelative(p.updated_at)}`);
  return pieces;
}

function sourcePieces(p: ApplicationPartView): string[] {
  return p.requests_last_day === undefined ? [] : [`${p.requests_last_day.toLocaleString()} requests in the last day`];
}

function lowerFirst(s: string): string {
  return s.length > 0 ? s[0]!.toLowerCase() + s.slice(1) : s;
}

/** partReference is the mcp: reference a part is cited by; a webhook source and a schema have none. */
export function partReference(kind: ApplicationPartKind, id: string): string | null {
  return kind === "source" || kind === "schema" || kind === "site" ? null : `mcp:${kind}:${id}`;
}

export const ACTION_LABEL: Record<ApplicationReleaseAction, string> = {
  release: "Release",
  rollback: "Roll back",
  import: "Import",
};

/** formatWhen renders a timestamp in the reader's locale, or a dash. */
export function formatWhen(iso?: string): string {
  if (!iso) return "—";
  const at = new Date(iso);
  if (Number.isNaN(at.getTime())) return "—";
  return at.toLocaleString();
}

/** versionLabel reads a pin's version; 0 is "not pinned" (a part new to a release). */
export function versionLabel(version: number): string {
  return version > 0 ? `v${version}` : "—";
}

/** errorText is what a failed call says to the reader. */
export function errorText(e: unknown, fallback: string): string {
  return e instanceof Error && e.message ? e.message : fallback;
}

/** partPath is where a part opens in the portal, when it has a page there. */
export function partPath(kind: ApplicationPartKind, id: string, isAdmin: boolean): string | null {
  switch (kind) {
    case "script":
      return `/automations/${encodeURIComponent(id)}`;
    case "asset":
      return `/assets/${encodeURIComponent(id)}`;
    case "source":
      // A webhook source's page is the administrator's.
      return isAdmin ? `/admin/webhooks/${encodeURIComponent(id)}` : null;
    case "site":
      // "<site>/<environment>": the part opens on the site's page.
      return `/sites/${encodeURIComponent(id.split("/")[0] ?? id)}`;
    case "schema":
      return null;
  }
}
