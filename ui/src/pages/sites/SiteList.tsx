import { ChevronRight, ExternalLink, Globe, Lock } from "lucide-react";
import { answersAnyone, statusOf, type Site, type SiteEnvironment, type SiteList as SiteListing } from "@/api/portal/hooks/sites";
import { OpenRowChevron, OpenRowHead } from "@/components/patterns/OpenRowChevron";
import { Badge } from "@/components/ui/badge";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { openRowProps } from "@/lib/openRow";
import { cn } from "@/lib/utils";
import { useListingLayout, type LayoutWidths, type ListingLayout } from "@/pages/scripts/useListingLayout";
import { bareAddress, envBadgeText, envTip, initials, orderedEnvironments } from "./siteFormat";
import { count, ms } from "./SiteTraffic";
import { listTileEnvironment, SiteTile } from "./SiteTile";

// The Sites list (#2559, #2612) at the layout the width it HAS fits, the way
// the Automations listing does (#2601): every row two lines, nothing cut at the
// card's edge, nothing scrolling sideways.
//
//   wide    Owner shows initials and the address;
//   medium  Owner shows initials only, the address on hover;
//   narrow  Owner drops out (it is on the site's page);
//   list    a phone, or a width no table fits.
//
// Name, description, owner and address are one line each, cut with an
// ellipsis and whole on hover; each environment is one badge; domains are on
// the site's page. Each row starts with the tile of the environment holding
// the main address, and the address is the production address, its public
// domain first (#2658). Every header ends inside its own column (#2654): the
// fixed columns are sized for their headers, which never wrap. A click anywhere on a row, or Enter on it, opens the site
// (#2587), and the address still opens the live site in a new tab.

/** The least width each table layout of the Sites list fits in. */
export const SITE_LAYOUT_WIDTHS: LayoutWidths = { wide: 1000, medium: 680, narrow: 480 };

const COLUMN_WIDTHS: Record<Exclude<ListingLayout, "list">, { tile: string; owner: string; address: string; envs: string; traffic: string }> = {
  wide: { tile: "w-[88px]", owner: "w-[13%]", address: "w-[21%]", envs: "w-[24%]", traffic: "w-[128px]" },
  medium: { tile: "w-[88px]", owner: "w-[64px]", address: "w-[22%]", envs: "w-[28%]", traffic: "w-[128px]" },
  narrow: { tile: "w-[80px]", owner: "", address: "w-[32%]", envs: "w-[34%]", traffic: "" },
};

/** How many environment badges a row shows before the rest fold into "+N", keeping a row at two lines.
 * Four take two lines of the Environments column at the widths above; a
 * narrower column wraps them to a third on Linux fonts (#2647). */
const BADGES: Record<Exclude<ListingLayout, "list">, number> = { wide: 4, medium: 4, narrow: 2 };

export function SiteTable({ list, onOpen, query = "" }: { list: SiteListing; onOpen: (site: string) => void; query?: string }) {
  const { ref, layout } = useListingLayout(SITE_LAYOUT_WIDTHS);
  if (layout === "list") {
    return (
      <div ref={ref}>
        <SitePhoneList sites={list.sites} onOpen={onOpen} query={query} />
      </div>
    );
  }
  const widths = COLUMN_WIDTHS[layout];
  const owner = layout !== "narrow";
  const traffic = layout !== "narrow";
  return (
    <div ref={ref} data-testid="site-table" data-layout={layout}>
      <Table className="table-fixed">
        <TableHeader>
          <TableRow>
            <TableHead className={widths.tile}>
              <span className="sr-only">Preview</span>
            </TableHead>
            <TableHead>Name</TableHead>
            {owner && <TableHead className={cn(widths.owner, "whitespace-nowrap")}>Owner</TableHead>}
            <TableHead className={cn(widths.address, "whitespace-nowrap")}>Production address</TableHead>
            {traffic && <TableHead className={cn(widths.traffic, "whitespace-nowrap")}>Requests (24h)</TableHead>}
            <TableHead className={cn(widths.envs, "whitespace-nowrap")}>Environments</TableHead>
            <OpenRowHead />
          </TableRow>
        </TableHeader>
        <TableBody>
          {list.sites.map((s) => (
            <TableRow key={s.id} {...openRowProps(() => onOpen(s.name))} data-testid={`site-row-${s.name}`}>
              <TableCell className="py-2">
                <SiteTile site={s} env={listTileEnvironment(s)} size="row" />
              </TableCell>
              <TableCell className="py-2.5">
                <NameLines site={s} />
              </TableCell>
              {owner && (
                <TableCell>
                  <Owner email={s.owner_email} withAddress={layout === "wide"} />
                </TableCell>
              )}
              <TableCell>
                <MainAddress site={s} query={query} />
              </TableCell>
              {traffic && (
                <TableCell>
                  <TrafficCell site={s} p95={layout === "wide"} />
                </TableCell>
              )}
              <TableCell className="whitespace-normal">
                <EnvironmentBadges site={s} max={BADGES[layout]} />
              </TableCell>
              <OpenRowChevron />
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}

/** SitePhoneList is the Sites list on a phone: one tap target per site, its tile, name, what it is, its address and its environments. */
function SitePhoneList({ sites, onOpen, query }: { sites: Site[]; onOpen: (site: string) => void; query: string }) {
  return (
    <ul className="-mx-4 divide-y border-t" data-testid="site-list">
      {sites.map((s) => {
        const open = openRowProps(() => onOpen(s.name));
        return (
          <li key={s.id}>
            <div {...open} className={cn(open.className, "flex min-w-0 gap-3 px-4 py-3")} data-testid={`site-row-${s.name}`}>
              <SiteTile site={s} env={listTileEnvironment(s)} size="phone" />
              <span className="flex min-w-0 flex-1 flex-col gap-1.5">
                <span className="flex min-w-0 items-center gap-2">
                  <span className="min-w-0 flex-1 truncate text-sm font-semibold">{s.name}</span>
                  <ChevronRight className="size-4 shrink-0 text-muted-foreground" aria-hidden />
                </span>
                {s.description && <span className="line-clamp-2 text-xs text-muted-foreground">{s.description}</span>}
                <MainAddress site={s} serves={false} query={query} />
                <EnvironmentBadges site={s} />
              </span>
            </div>
          </li>
        );
      })}
    </ul>
  );
}

function NameLines({ site }: { site: Site }) {
  return (
    <>
      <span className="block truncate font-medium">{site.name}</span>
      {site.description && (
        <span className="block truncate text-xs text-muted-foreground" title={site.description} data-testid="site-description">
          {site.description}
        </span>
      )}
    </>
  );
}

function Owner({ email, withAddress }: { email: string; withAddress: boolean }) {
  const letters = (
    <span
      role="img"
      aria-label={email}
      title={email}
      className="flex size-6 shrink-0 items-center justify-center rounded-full bg-muted text-[10px] font-semibold text-foreground"
    >
      {initials(email)}
    </span>
  );
  if (!withAddress) return letters;
  return (
    <span className="flex min-w-0 items-center gap-2">
      {letters}
      <span className="min-w-0 truncate" title={email} aria-hidden>
        {email}
      </span>
    </span>
  );
}

function MainAddress({ site, serves = true, query = "" }: { site: Site; serves?: boolean; query?: string }) {
  if (!site.address) return <span className="text-muted-foreground">None</span>;
  const text = bareAddress(site.address);
  const more = site.more_domains ?? 0;
  return (
    <span className="block min-w-0">
      <a
        className="flex min-w-0 items-center gap-1 font-mono text-[13px] text-primary hover:underline"
        href={site.address}
        target="_blank"
        rel="noreferrer"
        title={`${text}, opens in a new tab`}
        data-testid="site-address"
        onClick={(e) => e.stopPropagation()}
      >
        <span className="min-w-0 truncate">
          <Marked text={text} query={query} />
        </span>
        <ExternalLink className="h-3 w-3 shrink-0" aria-hidden />
      </a>
      {serves && site.main_environment && (
        <span className="block text-xs text-muted-foreground" data-testid="site-serves">
          serves {site.main_environment}
          {more > 0 && ` · ${more} more ${more === 1 ? "domain" : "domains"}`}
        </span>
      )}
    </span>
  );
}

/** Marked is text with the part a search matched marked. */
function Marked({ text, query }: { text: string; query: string }) {
  const q = query.trim().toLowerCase();
  const at = q ? text.toLowerCase().indexOf(q) : -1;
  if (at < 0) return <>{text}</>;
  return (
    <>
      {text.slice(0, at)}
      <mark className="rounded-sm bg-yellow-100 text-inherit dark:bg-yellow-900">{text.slice(at, at + q.length)}</mark>
      {text.slice(at + q.length)}
    </>
  );
}

/** EnvironmentBadges is one badge per environment, dev first and prod last; past max the rest fold into "+N". */
export function EnvironmentBadges({ site, max }: { site: Pick<Site, "environments">; max?: number }) {
  const envs = orderedEnvironments(site.environments);
  const shown = max === undefined ? envs : envs.slice(0, max);
  const rest = envs.slice(shown.length);
  return (
    <div className="flex flex-wrap gap-1">
      {shown.map((e) => (
        <EnvironmentBadge key={e.name} env={e} />
      ))}
      {rest.length > 0 && (
        <Badge variant="outline" title={rest.map(envTip).join("\n")}>
          +{rest.length}
        </Badge>
      )}
    </div>
  );
}

function EnvironmentBadge({ env }: { env: SiteEnvironment }) {
  const status = statusOf(env);
  if (status !== "online") {
    // Offline and Redirecting (#2631, #2632) say so in place of the release.
    const off = status === "offline";
    return (
      <Badge variant={off ? "danger" : "warning"} className="max-w-full" title={off ? `${env.name} is offline` : `${env.name} redirects to ${env.redirect_to ?? ""}`} data-testid={`env-badge-${env.name}`}>
        <span className="min-w-0 truncate">
          {env.name} {off ? "Offline" : "Redirecting"}
        </span>
      </Badge>
    );
  }
  return (
    <Badge
      variant={answersAnyone(env) ? "info" : "muted"}
      className={cn("max-w-full", env.release > 0 ? "" : "border-dashed border-border bg-transparent")}
      title={envTip(env)}
      data-testid={`env-badge-${env.name}`}
    >
      {answersAnyone(env) ? <Globe aria-hidden /> : <Lock aria-hidden />}
      <span className="min-w-0 truncate">{envBadgeText(env)}</span>
    </Badge>
  );
}

/** A site's last 24 hours as the list shows them (#2647): requests, and p95 where there is room. */
function TrafficCell({ site, p95 }: { site: Site; p95: boolean }) {
  const t = site.traffic;
  if (!t || t.requests_24h === 0) return <span className="text-muted-foreground">–</span>;
  return (
    <span className="block tabular-nums" data-testid="site-traffic">
      {count(t.requests_24h)}
      {p95 && <span className="block text-xs text-muted-foreground">p95 {ms(t.p95_ms)}</span>}
    </span>
  );
}
