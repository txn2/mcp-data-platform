import { useState } from "react";
import { siteTileURL } from "@/api/portal/hooks/siteList";
import { statusOf, type Site, type SiteEnvironment } from "@/api/portal/hooks/sites";
import { cn } from "@/lib/utils";

// SiteTile is an environment's tile (#2658): a picture of the release it
// serves, drawn from the release's own files. An environment with no release,
// offline, redirecting, or with no pages address shows a placeholder saying
// so rather than a picture of what it no longer serves; a tile not drawn yet
// for its current release says that, and one that could not be drawn says why
// (#2664).

/** tilePlaceholder is why an environment shows no picture, "" when it may. */
export function tilePlaceholder(site: Pick<Site, "pages_addresses">, env: SiteEnvironment | undefined): { label: string; why: string } | null {
  if (!env) return { label: "No environment", why: "The main address serves no environment." };
  const status = statusOf(env);
  if (status === "offline") return { label: "Offline", why: "Visitors get the offline page." };
  if (status === "redirect") return { label: "Redirecting", why: `It redirects to ${env.redirect_to ?? "another site"}.` };
  if (env.release === 0) return { label: "No release yet", why: "Promote a release here to see it." };
  if (!site.pages_addresses[env.name]) return { label: "No pages address", why: "It has no pages address to be drawn from." };
  if (env.tile_failure) return { label: "Can't draw", why: env.tile_failure };
  return null;
}

const BOXES = {
  row: "h-11 w-16 rounded-md",
  phone: "h-[50px] w-[72px] rounded-md",
  card: "aspect-[16/10] w-full rounded-lg",
} as const;

type TileSize = keyof typeof BOXES;

export function SiteTile({ site, env, size }: { site: Pick<Site, "name" | "pages_addresses">; env: SiteEnvironment | undefined; size: TileSize }) {
  const [missing, setMissing] = useState<number | null>(null);
  const notDrawn = env !== undefined && missing === env.release;
  const placeholder = tilePlaceholder(site, env) ?? (notDrawn ? { label: "Not drawn yet", why: "Its tile is being drawn." } : null);
  if (placeholder || !env) return <TilePlaceholder size={size} name={env?.name ?? "none"} placeholder={placeholder} />;
  return (
    <img
      src={`${siteTileURL(site.name, env.name)}?r=${env.release}`}
      alt={`${env.name} at release ${env.release}`}
      loading="lazy"
      className={cn(BOXES[size], "shrink-0 border bg-muted object-cover object-top")}
      onError={() => setMissing(env.release)}
      data-testid={`site-tile-${env.name}`}
    />
  );
}

function TilePlaceholder({ size, name, placeholder }: { size: TileSize; name: string; placeholder: { label: string; why: string } | null }) {
  const card = size === "card";
  return (
    <div
      className={cn(BOXES[size], "flex shrink-0 flex-col items-center justify-center gap-0.5 border border-dashed px-1 text-center")}
      title={card ? undefined : placeholder?.why}
      data-testid={`site-tile-placeholder-${name}`}
    >
      <span className={cn("leading-tight", card ? "text-sm font-medium" : "text-[10px] text-muted-foreground")}>{placeholder?.label}</span>
      {card && <span className="text-xs text-muted-foreground">{placeholder?.why}</span>}
    </div>
  );
}
