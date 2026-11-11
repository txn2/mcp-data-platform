import { SiteDetailPage, type SiteTab } from "./SiteDetailPage";
import { SiteFilePage } from "./SiteFilePage";
import { SiteReleasePage } from "./SiteReleasePage";
import { SitesPage } from "./SitesPage";

const TABS: SiteTab[] = ["environments", "files", "media", "releases", "access", "domains", "history"];

// SiteRoutes is the portal's Sites section (#2559): the listing, a site with
// its tabs, one release, and one of dev's files opened to edit (#2590).
export function SiteRoutes({ route, onNavigate }: { route: string; onNavigate: (path: string) => void }) {
  if (route === "/sites") return <SitesPage onNavigate={onNavigate} />;
  const release = route.match(/^\/sites\/([^/]+)\/releases\/(\d+)$/);
  if (release) {
    return <SiteReleasePage site={decodeURIComponent(release[1]!)} number={Number(release[2])} onNavigate={onNavigate} />;
  }
  const file = route.match(/^\/sites\/([^/]+)\/(file|newfile)\/(.+)$/);
  if (file) {
    return <SiteFilePage site={decodeURIComponent(file[1]!)} path={decodeURIComponent(file[3]!)} fresh={file[2] === "newfile"} onNavigate={onNavigate} />;
  }
  const detail = route.match(/^\/sites\/([^/]+)(?:\/([a-z]+))?$/);
  if (!detail) return null;
  const tab = TABS.find((t) => t === detail[2]) ?? "environments";
  return <SiteDetailPage site={decodeURIComponent(detail[1]!)} tab={tab} onNavigate={onNavigate} />;
}
