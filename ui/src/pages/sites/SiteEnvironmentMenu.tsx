import { useState } from "react";
import { MoreHorizontal } from "lucide-react";
import { DEV_ENVIRONMENT, PROD_ENVIRONMENT, statusOf, useRemoveSiteEnvironment, useSetSiteEnvironment, type Site, type SiteEnvironment } from "@/api/portal/hooks/sites";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useAuthStore } from "@/stores/auth";
import { envNameProblem } from "./envName";
import { Failure } from "./SiteParts";
import { bareAddress, errorText } from "./siteFormat";

// The operations on an environment beside promoting and rolling back
// (#2630, #2631, #2632): take it offline, redirect it, rename it, remove it.
// dev has none of them; prod is taken offline or redirected, and only the
// environments between them are renamed or removed.

export type MenuAction = "offline" | "redirect" | "rename" | "remove";

/** Whether an environment is one between dev and prod. */
function between(name: string): boolean {
  return name !== DEV_ENVIRONMENT && name !== PROD_ENVIRONMENT;
}

/** The More menu on an environment's card; none on dev. */
export function EnvironmentMenu({ env, onAct }: { env: SiteEnvironment; onAct: (a: MenuAction) => void }) {
  if (env.name === DEV_ENVIRONMENT) return null;
  const status = statusOf(env);
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button size="sm" variant="outline" aria-label={`More for ${env.name}`}>
          <MoreHorizontal className="h-4 w-4" aria-hidden />
          More
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        {status !== "offline" && <DropdownMenuItem onSelect={() => onAct("offline")}>Take offline</DropdownMenuItem>}
        <DropdownMenuItem onSelect={() => onAct("redirect")}>{status === "redirect" ? "Change redirect" : "Redirect"}</DropdownMenuItem>
        {between(env.name) ? (
          <>
            <DropdownMenuSeparator />
            <DropdownMenuItem onSelect={() => onAct("rename")}>Rename</DropdownMenuItem>
            <DropdownMenuItem className="text-destructive" onSelect={() => onAct("remove")}>
              Remove
            </DropdownMenuItem>
          </>
        ) : (
          <DropdownMenuLabel className="max-w-56 text-xs font-normal text-muted-foreground">Every site has dev and prod, so prod can't be renamed or removed.</DropdownMenuLabel>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

/** The address an environment would answer at under name, on the pages domain. */
function addressUnder(site: Site, from: string, to: string): string {
  const now = bareAddress(site.pages_addresses?.[from] ?? "");
  const marker = `${site.name}--${from}.`;
  return now.startsWith(marker) ? `${site.name}--${to}.${now.slice(marker.length)}` : "";
}

/** Renames an environment between dev and prod (#2630). */
export function RenameDialog({ site, env, onClose }: { site: Site; env: SiteEnvironment; onClose: () => void }) {
  const set = useSetSiteEnvironment(site.name);
  const [name, setName] = useState("");
  const [failure, setFailure] = useState<string | null>(null);
  const clean = name.trim();
  const problem = clean ? envNameProblem(site.name, clean) : "";
  const oldAddress = bareAddress(site.pages_addresses?.[env.name] ?? "");
  const submit = () => {
    setFailure(null);
    set.mutate({ name: env.name, rename_to: clean }, { onSuccess: onClose, onError: (e) => setFailure(errorText(e, "The environment could not be renamed")) });
  };
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Rename {env.name}</DialogTitle>
          <DialogDescription>It keeps its place, settings, release, domains, shares and history.</DialogDescription>
        </DialogHeader>
        <div className="space-y-3">
          <div className="space-y-1">
            <Label htmlFor="rename-to">New name</Label>
            <Input id="rename-to" value={name} onChange={(e) => setName(e.target.value)} aria-invalid={problem !== ""} />
            {problem && <p className="text-xs text-destructive">{problem}</p>}
          </div>
          {oldAddress && (
            <div className="space-y-1 text-xs">
              <p>
                <span className="text-muted-foreground">Now </span>
                <span className="break-all font-mono">{oldAddress}</span>
              </p>
              {clean && !problem && (
                <p>
                  <span className="text-muted-foreground">After </span>
                  <span className="break-all font-mono">{addressUnder(site, env.name, clean)}</span>
                </p>
              )}
            </div>
          )}
          <p className="text-xs text-muted-foreground">
            The old address stops working at once, and <span className="font-mono">mcp:site:{site.name}/{env.name}</span> references written in assets, knowledge pages or scripts stop resolving. Domains added to {env.name} keep answering.
          </p>
          <Failure text={failure} />
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={set.isPending || !clean || problem !== ""}>
            Rename
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/** What stands in the way of removing an environment, from what the card knows. */
function obstacles(site: Site, env: SiteEnvironment, admin: boolean): string[] {
  const out: string[] = [];
  const domains = site.domains.filter((d) => d.environment === env.name);
  if (domains.length > 0) out.push(`It has ${domains.length === 1 ? "a domain" : `${domains.length} domains`} (${domains.map((d) => d.hostname).join(", ")}). Remove ${domains.length === 1 ? "it" : "them"} on the Domains tab first.`);
  if (site.main_environment === env.name) out.push("It holds the main address. Give the main address to another environment first.");
  if (env.requires_approval && !admin) out.push("It requires approval, so only an administrator can remove it.");
  return out;
}

/** Removes an environment between dev and prod (#2630). */
export function RemoveDialog({ site, env, onClose }: { site: Site; env: SiteEnvironment; onClose: () => void }) {
  const remove = useRemoveSiteEnvironment(site.name);
  const admin = useAuthStore((s) => s.user?.is_admin === true);
  const [failure, setFailure] = useState<string | null>(null);
  const blocked = obstacles(site, env, admin);
  const i = site.environments.findIndex((e) => e.name === env.name);
  const before = site.environments[i - 1]?.name ?? DEV_ENVIRONMENT;
  const after = site.environments[i + 1]?.name ?? PROD_ENVIRONMENT;
  const submit = () => {
    setFailure(null);
    remove.mutate(env.name, { onSuccess: onClose, onError: (e) => setFailure(errorText(e, "The environment could not be removed")) });
  };
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Remove {env.name}</DialogTitle>
          <DialogDescription>{site.name}</DialogDescription>
        </DialogHeader>
        {blocked.length > 0 ? (
          <div className="space-y-2" data-testid="remove-blocked">
            <p className="text-sm font-medium">{env.name} can't be removed yet</p>
            <ul className="list-disc space-y-1 pl-5 text-sm">
              {blocked.map((b) => (
                <li key={b}>{b}</li>
              ))}
            </ul>
          </div>
        ) : (
          <div className="space-y-2 text-sm">
            <p>What goes:</p>
            <ul className="list-disc space-y-1 pl-5">
              <li>Its address, at once.</li>
              <li>Releases only {env.name} keeps, at the next cleanup.</li>
              <li>Shares that name only {env.name}. Shares that also name another environment keep the others.</li>
              <li>Pending approvals for {env.name}, withdrawn.</li>
            </ul>
            <p className="text-muted-foreground">
              {after} then takes releases from {before}. Its history stays, marked removed.
            </p>
            <Failure text={failure} />
          </div>
        )}
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            {blocked.length > 0 ? "Close" : "Cancel"}
          </Button>
          {blocked.length === 0 && (
            <Button variant="destructive" onClick={submit} disabled={remove.isPending}>
              Remove {env.name}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
