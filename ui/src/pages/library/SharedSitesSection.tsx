import { ExternalLink } from "lucide-react";
import { useSharedSites, type SharedSiteEnvironment } from "@/api/portal/hooks/sharedSites";

/** hostOf is an address without its scheme, as the row shows it. */
export function hostOf(address: string): string {
  return address.replace(/^https?:\/\//, "").replace(/\/$/, "");
}

/**
 * SharedSitesSection lists on Home the site environments shared with the
 * reader (#2718), each opening in a new tab where the site signs them in.
 * Only what a grant opens is listed, and a reader with none sees no section:
 * people who never use sites never see an empty box.
 */
export function SharedSitesSection() {
  const { data } = useSharedSites();
  const envs = data ?? [];
  if (envs.length === 0) return null;
  return (
    <section aria-label="Sites shared with you" className="flex flex-col gap-2 rounded-xl border bg-card p-4">
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <h2 className="text-base font-semibold">Sites shared with you</h2>
        <span className="text-xs text-muted-foreground">Opens in a new tab, signed in as you</span>
      </div>
      <ul className="flex flex-col">
        {envs.map((e) => (
          <li key={`${e.site}/${e.environment}`} className="border-b last:border-b-0">
            <SharedSiteRow env={e} />
          </li>
        ))}
      </ul>
    </section>
  );
}

function SharedSiteRow({ env }: { env: SharedSiteEnvironment }) {
  const label = (
    <span className="flex min-w-0 flex-col gap-0.5 md:grid md:flex-1 md:grid-cols-[minmax(0,1.2fr)_minmax(0,0.6fr)_minmax(0,1.6fr)] md:items-center md:gap-4">
      <span className="flex min-w-0 items-center gap-2 font-medium">
        <span className="truncate">{env.site}</span>
        <span className="shrink-0 rounded-full bg-muted px-2 py-0.5 text-xs font-normal text-muted-foreground md:hidden">
          {env.environment}
        </span>
      </span>
      <span className="hidden md:block">
        <span className="rounded-full bg-muted px-2 py-0.5 text-xs text-muted-foreground">{env.environment}</span>
      </span>
      <span className="truncate text-xs text-muted-foreground md:text-sm">
        {env.online ? hostOf(env.address) || "No address yet" : env.message || "The site's owner has taken this environment offline."}
      </span>
    </span>
  );
  if (!env.online || !env.address) {
    return (
      <div className="flex min-h-14 items-center justify-between gap-3 py-2 text-sm text-muted-foreground">
        {label}
        {!env.online && (
          <span className="shrink-0 rounded-full bg-destructive/10 px-2.5 py-0.5 text-xs font-semibold text-destructive">Offline</span>
        )}
      </div>
    );
  }
  return (
    <a
      href={env.address}
      target="_blank"
      rel="noopener noreferrer"
      className="flex min-h-14 items-center justify-between gap-3 py-2 text-sm hover:text-primary"
    >
      {label}
      <span className="flex shrink-0 items-center gap-1 text-muted-foreground md:min-h-9 md:rounded-lg md:border md:px-3 md:text-foreground">
        <span className="hidden md:inline">Open</span>
        <ExternalLink className="h-4 w-4" aria-hidden="true" />
        <span className="sr-only">Open {env.site} {env.environment} in a new tab</span>
      </span>
    </a>
  );
}
