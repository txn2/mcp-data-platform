import { useEffect, useState, type ReactNode } from "react";
import { Globe } from "lucide-react";
import { SITES_PAGE, useSitePage } from "@/api/portal/hooks/siteList";
import { useCreateSite, useSiteNameCheck, type SiteList, type SiteNameCheck } from "@/api/portal/hooks/sites";
import { EmptyState } from "@/components/patterns/EmptyState";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import { Textarea } from "@/components/ui/textarea";
import { SiteTable } from "./SiteList";
import { ReplicasSection } from "./SiteTraffic";
import { bareAddress, errorText } from "./siteFormat";

// SitesPage lists the sites (#2559, #2604, #2612, #2658): who owns each, its
// production address and the environment that serves it, and each environment
// with the release it serves and whether it is private or public (SiteList).
// The search matches a name, description, owner or domain on the server, is
// kept in the address so a filtered list can be linked and survives a reload,
// and the list is paged there too, so hundreds of sites cost one page.

/** readQuery is the search a page address carries. */
function readQuery(): string {
  return typeof window === "undefined" ? "" : (new URLSearchParams(window.location.search).get("q") ?? "");
}

/** keepQuery puts the search in the page address without a new history entry. */
function keepQuery(q: string) {
  const url = new URL(window.location.href);
  if (q) url.searchParams.set("q", q);
  else url.searchParams.delete("q");
  window.history.replaceState(null, "", url);
}

/** useSiteSearch is the typed search, the one asked of the server a moment
 * after typing stops, and the page. */
function useSiteSearch() {
  const [typed, setTyped] = useState(readQuery);
  const [query, setQuery] = useState(typed.trim());
  const [offset, setOffset] = useState(0);
  useEffect(() => {
    const t = setTimeout(() => {
      setQuery(typed.trim());
      setOffset(0);
      keepQuery(typed.trim());
    }, 250);
    return () => clearTimeout(t);
  }, [typed]);
  return { typed, setTyped, query, offset, setOffset };
}

export function SitesPage({ onNavigate }: { onNavigate: (path: string) => void }) {
  const search = useSiteSearch();
  const { data, isLoading, error } = useSitePage(search.query, search.offset);
  const [creating, setCreating] = useState(false);
  const open = (site: string) => onNavigate(`/sites/${encodeURIComponent(site)}`);
  const newSite = data?.can_create ? (
    <Button size="sm" onClick={() => setCreating(true)}>
      New site
    </Button>
  ) : null;

  return (
    <div className="space-y-4">
      {data && !data.pages_domain && <PreviewsOff admin={data.admin} onNavigate={onNavigate} />}
      <SectionCard
        title="Sites"
        action={
          <div className="flex flex-wrap items-center gap-2">
            <Input
              type="search"
              aria-label="Search sites"
              placeholder="Search by name, description, owner or domain"
              className="h-9 w-full sm:w-80"
              value={search.typed}
              onChange={(e) => search.setTyped(e.target.value)}
            />
            {newSite}
          </div>
        }
      >
        <SitesBody
          list={data}
          loading={isLoading}
          error={error}
          newSite={newSite}
          onOpen={open}
          query={search.query}
          onClear={() => search.setTyped("")}
          offset={search.offset}
          onPage={search.setOffset}
        />
      </SectionCard>
      {data?.admin && <ReplicasSection />}
      {creating && data && (
        <NewSiteDialog
          onClose={() => setCreating(false)}
          onCreated={(name) => {
            setCreating(false);
            open(name);
          }}
        />
      )}
    </div>
  );
}

function SitesBody({
  list,
  loading,
  error,
  newSite,
  onOpen,
  query,
  onClear,
  offset,
  onPage,
}: {
  list?: SiteList;
  loading: boolean;
  error: Error | null;
  newSite: ReactNode;
  onOpen: (site: string) => void;
  query: string;
  onClear: () => void;
  offset: number;
  onPage: (offset: number) => void;
}) {
  if (error) {
    return (
      <Alert variant="destructive">
        <AlertDescription>{error.message}</AlertDescription>
      </Alert>
    );
  }
  if (loading || !list) return <SitesLoading />;
  if (list.sites.length === 0 && query) {
    return (
      <div className="flex flex-col items-center gap-3 rounded-lg border border-dashed p-10 text-center" data-testid="sites-none-matched">
        <p className="text-base font-medium">No site matches {query}</p>
        <p className="text-sm text-muted-foreground">Nothing in a site&apos;s name, description, owner or domains holds {query}.</p>
        <Button variant="outline" size="sm" onClick={onClear}>
          Clear the search
        </Button>
      </div>
    );
  }
  if (list.sites.length === 0) {
    return (
      <EmptyState icon={Globe} action={newSite}>
        <p className="font-medium text-foreground">No sites</p>
        <p className="max-w-md text-xs">A site is a set of web pages this platform serves on its own address. Create one, then upload its files or copy a site that already exists.</p>
      </EmptyState>
    );
  }
  return (
    <div className="space-y-3">
      <p className="text-xs text-muted-foreground">A new site starts with dev, its working copy, and prod, on its main address. Promote releases one step at a time when people should use them.</p>
      <SiteTable list={list} onOpen={onOpen} query={query} />
      <SitesPager total={list.total} shown={list.sites.length} offset={offset} query={query} onPage={onPage} />
    </div>
  );
}

/** SitesPager says which sites the page shows and moves between pages. */
function SitesPager({ total, shown, offset, query, onPage }: { total: number; shown: number; offset: number; query: string; onPage: (o: number) => void }) {
  const what = query ? `${total === 1 ? "site matches" : "sites match"} ${query}` : total === 1 ? "site" : "sites";
  return (
    <div className="flex flex-wrap items-center justify-between gap-2 text-sm text-muted-foreground" data-testid="sites-pager">
      <span>
        {total <= SITES_PAGE ? `${total} ${what}` : `${offset + 1} to ${offset + shown} of ${total} ${what}`}
      </span>
      {total > SITES_PAGE && (
        <div className="flex gap-2">
          <Button variant="outline" size="sm" disabled={offset === 0} onClick={() => onPage(Math.max(0, offset - SITES_PAGE))}>
            Previous
          </Button>
          <Button variant="outline" size="sm" disabled={offset + shown >= total} onClick={() => onPage(offset + SITES_PAGE)}>
            Next
          </Button>
        </div>
      )}
    </div>
  );
}

function SitesLoading() {
  return (
    <div aria-busy="true" data-testid="sites-loading">
      <span className="sr-only">Loading sites</span>
      {[0, 1, 2].map((i) => (
        <div key={i} className="grid grid-cols-[minmax(0,1fr)_22%_26%] gap-4 border-b px-2 py-3.5">
          <div className="space-y-1.5">
            <Skeleton className="h-3 w-2/5" />
            <Skeleton className="h-2.5 w-4/5" />
          </div>
          <Skeleton className="h-3 w-11/12" />
          <Skeleton className="h-3 w-full" />
        </div>
      ))}
    </div>
  );
}

function PreviewsOff({ admin, onNavigate }: { admin: boolean; onNavigate: (path: string) => void }) {
  return (
    <Alert data-testid="previews-off">
      <AlertTitle>Previews are off</AlertTitle>
      <AlertDescription className="flex flex-wrap items-center justify-between gap-2">
        {admin ? (
          <>
            <span>This deployment has no pages domain, so a site has no address of its own. A domain added to a site still serves.</span>
            <Button size="sm" variant="outline" onClick={() => onNavigate("/admin/settings/sites")}>
              Set a pages domain
            </Button>
          </>
        ) : (
          <span>A site has no address of its own on this deployment until an administrator sets a pages domain.</span>
        )}
      </AlertDescription>
    </Alert>
  );
}

/** value after it has stopped changing for ms. */
function useSettled(value: string, ms: number): string {
  const [settled, setSettled] = useState(value);
  useEffect(() => {
    const t = window.setTimeout(() => setSettled(value), ms);
    return () => window.clearTimeout(t);
  }, [value, ms]);
  return settled;
}

// NewSiteDialog takes the name a person types and shows the address the server
// makes of it (#2636): the rule for a site's name lives on the server alone,
// which answers with the name, both addresses and why it is refused.
function NewSiteDialog({ onClose, onCreated }: { onClose: () => void; onCreated: (name: string) => void }) {
  const create = useCreateSite();
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [failure, setFailure] = useState<string | null>(null);
  const typed = name.trim();
  const answer = useTypedNameCheck(typed);
  const refusal = answer?.refusal ?? "";
  const submit = () => {
    setFailure(null);
    create.mutate(
      { name: typed, description: description.trim() },
      { onSuccess: (s) => onCreated(s.name), onError: (e) => setFailure(errorText(e, "The site could not be created")) },
    );
  };
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>New site</DialogTitle>
        </DialogHeader>
        <div className="space-y-3">
          <NameField name={name} onChange={setName} typed={typed} answer={answer} />
          <div className="space-y-1">
            <Label htmlFor="site-description">Description</Label>
            <Textarea id="site-description" value={description} onChange={(e) => setDescription(e.target.value)} />
          </div>
          {answer && !refusal && <NewSiteAddresses answer={answer} />}
          {failure && (
            <Alert variant="destructive">
              <AlertDescription>{failure}</AlertDescription>
            </Alert>
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={!answer || refusal !== "" || create.isPending}>
            Create site
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/** The server's answer for the text typed, once it has settled; none while it is checked. */
function useTypedNameCheck(typed: string): SiteNameCheck | undefined {
  const settled = useSettled(typed, 250);
  const check = useSiteNameCheck(settled);
  return typed !== "" && settled === typed ? check.data : undefined;
}

function NameField({ name, onChange, typed, answer }: { name: string; onChange: (v: string) => void; typed: string; answer?: SiteNameCheck }) {
  const refusal = answer?.refusal ?? "";
  return (
    <div className="space-y-1">
      <Label htmlFor="site-name">Name</Label>
      <Input
        id="site-name"
        value={name}
        onChange={(e) => onChange(e.target.value)}
        autoFocus
        placeholder="My Website"
        aria-invalid={refusal ? true : undefined}
        aria-describedby={refusal ? "site-name-error" : "site-name-address"}
      />
      {refusal ? (
        <p id="site-name-error" className="text-xs text-destructive">
          {refusal}
        </p>
      ) : (
        <NameAddress typed={typed} answer={answer} />
      )}
    </div>
  );
}

// NameAddress is the address a typed name becomes, said in words: the part of
// every address that cannot change later.
function NameAddress({ typed, answer }: { typed: string; answer?: SiteNameCheck }) {
  if (!typed) return <p id="site-name-address" className="text-xs text-muted-foreground">Name it as you like; the address is made from it.</p>;
  if (!answer) return <p id="site-name-address" className="text-xs text-muted-foreground">Checking…</p>;
  return (
    <p id="site-name-address" className="text-xs text-muted-foreground" data-testid="site-name-address">
      Address: <span className="font-mono text-foreground">{answer.name}</span>. It is part of every address the site has and can't be changed later.
    </p>
  );
}

// NewSiteAddresses is where a new site's two environments answer (#2617): dev
// at its own address and prod at the main address, as the server makes them.
function NewSiteAddresses({ answer }: { answer: SiteNameCheck }) {
  const dev = bareAddress(answer.addresses.dev ?? "");
  const prod = bareAddress(answer.addresses.prod ?? "");
  if (!dev && !prod) return null;
  return (
    <div className="space-y-1" data-testid="new-site-addresses">
      <Label>Environments</Label>
      <p className="text-sm">
        <span className="font-medium">dev</span> <span className="break-all font-mono text-muted-foreground">{dev}</span>
      </p>
      <p className="text-sm">
        <span className="font-medium">prod</span> <span className="break-all font-mono text-muted-foreground">{prod}</span> <span className="text-xs text-muted-foreground">(main address)</span>
      </p>
      <p className="text-xs text-muted-foreground">
        Every publish lands in dev; promote dev's release to prod when people should use it. Both addresses ask for a sign-in. To open the site to anyone, add a domain and make it public.
      </p>
    </div>
  );
}
