import { SITE_ROUTES, SITE_TABS } from "@/lib/portalRoutes";
import { SiteDetailPage } from "./SiteDetailPage";
import { SiteFilePage } from "./SiteFilePage";
import { SiteReleasePage } from "./SiteReleasePage";
import { SitesPage } from "./SitesPage";

// SiteRoutes is the portal's Sites section (#2559): the listing, a site with
// its tabs, one release, and one of dev's files opened to edit (#2590). It
// matches with the patterns the shell knows routes by (#2622), so an address
// it renders is never also the not-found page, and one it has no page for
// renders nothing under that page.
export function SiteRoutes({ route, onNavigate }: { route: string; onNavigate: (path: string) => void }) {
  if (route === "/sites") return <SitesPage onNavigate={onNavigate} />;
  const release = route.match(SITE_ROUTES.release);
  if (release) {
    return <SiteReleasePage site={decodeURIComponent(release[1]!)} number={Number(release[2])} onNavigate={onNavigate} />;
  }
  const file = route.match(SITE_ROUTES.file);
  if (file) {
    return <SiteFilePage site={decodeURIComponent(file[1]!)} path={decodeURIComponent(file[3]!)} fresh={file[2] === "newfile"} onNavigate={onNavigate} />;
  }
  const detail = route.match(SITE_ROUTES.site);
  if (!detail) return null;
  const tab = SITE_TABS.find((t) => t === detail[2]) ?? "environments";
  return <SiteDetailPage site={decodeURIComponent(detail[1]!)} tab={tab} onNavigate={onNavigate} />;
}
