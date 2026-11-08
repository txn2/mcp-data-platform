import type {
  ApplicationDiagram,
  ApplicationDiagramChange,
  ApplicationDiagramLink,
  ApplicationDiagramNode,
  ApplicationLinkKind,
  ApplicationNodeKind,
  ApplicationRelease,
} from "@/api/portal/hooks/applications";
import { CHANGE_COLOR, FONT_BODY, FONT_CHIP, FONT_MONO, FONT_TITLE } from "@/pages/scripts/flow/flowModel";

// The pure half of an application's Diagram tab (#2540): how a node's card is
// worded and sized, what a link is labeled, what a selected node comes from
// and goes to, and the versions the picker offers. The layout and the drawing
// read everything they need from here.

export const NODE_KIND_LABEL: Record<ApplicationNodeKind, string> = {
  script: "Automation",
  library: "Library",
  asset: "Asset",
  source: "Webhook source",
  schema: "Schema",
  table: "Table",
  channel: "Channel",
  output: "Output",
  endpoint: "Endpoint",
};

// NODE_COLOR is each kind's bar, from the portal's chart palette.
export const NODE_COLOR: Record<ApplicationNodeKind, string> = {
  script: "hsl(var(--chart-1))",
  library: "hsl(var(--chart-1))",
  asset: "hsl(var(--chart-3))",
  source: "hsl(var(--chart-5))",
  schema: "hsl(var(--chart-2))",
  table: "hsl(var(--chart-2))",
  channel: "hsl(var(--chart-4))",
  output: "hsl(var(--chart-3))",
  endpoint: "hsl(var(--chart-5))",
};

/** changeColor is the color a compared node or link is marked in; none when unchanged. */
export function changeColor(change: ApplicationDiagramChange | undefined): string | undefined {
  if (change === "added" || change === "removed" || change === "changed") return CHANGE_COLOR[change];
  return undefined;
}

// Card geometry, in CSS pixels at zoom 1.
export const NODE_WIDTH = 230;
const NODE_HEAD = 30;
const LINE_HEIGHT = 17;
const NODE_PAD = 10;
const CHIP_ROW = 22;
const MAX_DETAIL = 4;

export { FONT_BODY, FONT_CHIP, FONT_MONO, FONT_TITLE };

/** nodeSubtitle is the line under a card's name: its kind and the version drawn. */
export function nodeSubtitle(n: ApplicationDiagramNode): string {
  const kind = NODE_KIND_LABEL[n.kind] ?? n.kind;
  return n.version ? `${kind} · v${n.version}` : kind;
}

/** nodeDetail is the card's detail lines, as many as a card shows. */
export function nodeDetail(n: ApplicationDiagramNode): string[] {
  return (n.detail ?? []).slice(0, MAX_DETAIL);
}

/** nodeChips is what a card says along its bottom edge: computed, and how a comparison marks it. */
export function nodeChips(n: ApplicationDiagramNode): string[] {
  const out: string[] = [];
  if (n.computed) out.push("computed");
  if (n.change === "changed") out.push(`v${n.was_version ?? 0} → v${n.version ?? 0}`);
  else if (n.change === "added" || n.change === "removed") out.push(n.change);
  return out;
}

export function nodeHeight(n: ApplicationDiagramNode): number {
  const lines = 1 + nodeDetail(n).length;
  return NODE_HEAD + lines * LINE_HEIGHT + (nodeChips(n).length ? CHIP_ROW : 0) + NODE_PAD;
}

/**
 * linkLabel is what a link is labeled: its kind and its detail, and for a
 * compared link whose detail moved, what it said before.
 */
export function linkLabel(l: ApplicationDiagramLink): string {
  if (l.change === "changed") return [l.kind, l.was_detail ?? "", "→", l.detail ?? ""].filter(Boolean).join(" ");
  return l.detail ? `${l.kind} ${l.detail}` : l.kind;
}

/** A link seen from one of its ends: its kind and the node at the other end. */
export interface Relation {
  kind: ApplicationLinkKind;
  /** label is the link's kind and detail, as the diagram labels it. */
  label: string;
  other: ApplicationDiagramNode;
  change?: ApplicationDiagramChange;
}

/**
 * relations is what a node comes from (the links that end at it) and goes to
 * (the links that start at it), each with the node at the other end.
 */
export function relations(d: ApplicationDiagram, id: string): { from: Relation[]; to: Relation[] } {
  const byId = new Map(d.nodes.map((n) => [n.id, n]));
  const from: Relation[] = [];
  const to: Relation[] = [];
  for (const l of d.links) {
    const other = byId.get(l.to === id ? l.from : l.to);
    if (!other || (l.from !== id && l.to !== id)) continue;
    const r = { kind: l.kind, label: linkLabel(l), other, change: l.change };
    (l.to === id ? from : to).push(r);
  }
  return { from, to };
}

/** flowPath is where a node opens its automation's Flow tab, for an automation or library part. */
export function flowPath(n: ApplicationDiagramNode): string | null {
  if ((n.kind !== "script" && n.kind !== "library") || !n.part_id) return null;
  return `/automations/${encodeURIComponent(n.part_id)}`;
}

/** A version the picker offers: what the request names, and its words. */
export interface VersionChoice {
  value: string;
  label: string;
}

/** versionChoices is the current release, the latest saved versions, and each release. */
export function versionChoices(releases: ApplicationRelease[]): VersionChoice[] {
  return [
    { value: "current", label: "Current release" },
    { value: "latest", label: "Latest saved" },
    ...releases.map((r) => ({ value: String(r.number), label: `Release ${r.number}` })),
  ];
}

/** defaultVersion is the current release, or the latest saved versions before the first release. */
export function defaultVersion(currentRelease: number): string {
  return currentRelease > 0 ? "current" : "latest";
}

/**
 * defaultCompare is the release a comparison starts from: the newest release
 * older than the one drawn (the current release for the latest saved
 * versions), else the newest other release, else none.
 */
export function defaultCompare(version: string, currentRelease: number, releases: ApplicationRelease[]): string {
  const drawn = drawnRelease(version, currentRelease);
  const older = releases.find((r) => r.number < drawn);
  const other = older ?? releases.find((r) => r.number !== drawn);
  return other ? String(other.number) : "";
}

// drawnRelease is the release number a version stands for; the latest saved
// versions stand past every release.
function drawnRelease(version: string, currentRelease: number): number {
  if (version === "latest") return Number.MAX_SAFE_INTEGER;
  if (version === "current") return currentRelease;
  return Number(version);
}

// Where the chosen tab is remembered, per viewer. Best effort: a browser with
// no storage, or one refusing it, opens on List.
const TAB_KEY = "application-parts-tab";

export type PartsTab = "list" | "diagram";

export function storedPartsTab(): PartsTab {
  try {
    return globalThis.localStorage?.getItem(TAB_KEY) === "diagram" ? "diagram" : "list";
  } catch {
    return "list";
  }
}

export function storePartsTab(tab: PartsTab) {
  try {
    globalThis.localStorage?.setItem(TAB_KEY, tab);
  } catch {
    /* remembering the tab is best-effort */
  }
}
