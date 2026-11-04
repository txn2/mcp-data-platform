import type {
  ApplicationPartKind,
  ApplicationReleaseAction,
} from "@/api/portal/hooks/applications";

// The words the Applications pages show for the values the API carries. A
// script is called an automation in the portal (#1912), so a script part is
// named the way the Automations section names it.

export const PART_KIND_LABEL: Record<ApplicationPartKind, string> = {
  script: "Automation",
  asset: "Asset",
  source: "Webhook source",
};

export const PART_KINDS: ApplicationPartKind[] = ["script", "asset", "source"];

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
  }
}
