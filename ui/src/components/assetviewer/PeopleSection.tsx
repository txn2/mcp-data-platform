import { useState } from "react";
import { ChevronDown, ChevronRight } from "lucide-react";
import {
  useAssetPeople,
  useAssetShareSummary,
  useDirectoryNames,
  type AssetShareSummary,
} from "@/api/portal/hooks";
import { formatRelative } from "@/components/feedback/meta";
import { Input } from "@/components/ui/input";
import { usePortalAccess } from "@/hooks/usePortalAccess";

// PeopleSection is who is behind an asset (#2531), under Details in the
// viewer's sidebar: its owner, the other people who wrote a version, and, for
// the organization's own people, whom it is shared with, closed by default.
// The share summary carries no token or link address; those stay with the
// owner, in Share.

/** How many shared-with entries a group shows before "Show N more". */
const SHOWN = 10;

const LINK_LABEL: Record<AssetShareSummary["links"][number]["access_mode"], string> = {
  public: "Public link",
  authenticated: "Signed-in link",
  restricted: "Restricted link",
};

const PERMISSION_LABEL = { viewer: "Viewer", editor: "Editor" } as const;

function initials(name: string): string {
  const words = name.replace(/@.*/, "").split(/[\s._-]+/).filter(Boolean);
  return ((words[0]?.[0] ?? "") + (words[1]?.[0] ?? "")).toUpperCase() || "?";
}

/** displayName is how a contributor is named: a person, or an automation. */
function displayName(email: string, names: Record<string, string>): string {
  if (email.startsWith("script:")) return `Automation ${email.slice("script:".length)}`;
  return names[email.toLowerCase()] || email;
}

export function PeopleSection({ assetId }: { assetId: string }) {
  const { data } = useAssetPeople(assetId);
  const { external } = usePortalAccess();
  const names = useDirectoryNames(data ? [data.owner.email, ...data.contributors.map((c) => c.email)] : []);
  if (!data) return null;
  const versions = data.contributors.reduce((n, c) => n + c.versions, 0);
  const people = 1 + data.contributors.length;
  const ownerName = displayName(data.owner.email, names);
  return (
    <section className="space-y-3 border-t pt-4" aria-labelledby="asset-people-h" data-testid="asset-people">
      <div className="flex items-center justify-between">
        <h3 id="asset-people-h" className="text-sm font-medium">
          People
        </h3>
        <span className="text-xs text-muted-foreground">
          {people === 1 ? "1 person" : `${people} people`}
          {versions > 0 && ` · ${versions === 1 ? "1 version" : `${versions} versions`} by others`}
        </span>
      </div>
      <div className="flex items-center gap-2.5" data-testid="asset-owner">
        <Avatar name={ownerName} size="size-8" />
        <div className="flex min-w-0 flex-1 flex-col">
          <span className="truncate text-sm font-medium">{ownerName}</span>
          {ownerName !== data.owner.email && (
            <span className="truncate text-xs text-muted-foreground">{data.owner.email}</span>
          )}
        </div>
        <span className="rounded-full bg-muted px-2.5 py-0.5 text-xs">Owner</span>
      </div>
      {data.contributors.length > 0 && (
        <div className="space-y-2" data-testid="asset-contributors">
          <span className="text-xs font-medium text-muted-foreground">Contributors</span>
          {data.contributors.map((c) => {
            const name = displayName(c.email, names);
            return (
              <div key={c.email} className="flex items-center gap-2.5 text-sm">
                <Avatar name={name} size="size-7" />
                <span className="min-w-0 flex-1 truncate" title={c.email}>
                  {name}
                </span>
                <span className="shrink-0 text-xs text-muted-foreground">
                  {c.versions === 1 ? "1 version" : `${c.versions} versions`} · {formatRelative(c.last_change)}
                </span>
              </div>
            );
          })}
        </div>
      )}
      {!external && <SharedWith assetId={assetId} />}
    </section>
  );
}

function Avatar({ name, size }: { name: string; size: string }) {
  return (
    <span
      aria-hidden="true"
      className={`flex ${size} shrink-0 items-center justify-center rounded-full bg-muted text-[11px] font-semibold text-muted-foreground`}
    >
      {initials(name)}
    </span>
  );
}

/** breakdown is the closed block's line: "2 personas · 19 people · 2 links". */
export function breakdown(d: AssetShareSummary): string {
  const count = (n: number, one: string, many: string) => (n === 0 ? null : n === 1 ? `1 ${one}` : `${n} ${many}`);
  return [count(d.personas.length, "persona", "personas"), count(d.people.length, "person", "people"), count(d.links.length, "link", "links")]
    .filter(Boolean)
    .join(" · ");
}

function SharedWith({ assetId }: { assetId: string }) {
  const [open, setOpen] = useState(false);
  const { data } = useAssetShareSummary(assetId, true);
  if (!data || data.total === 0) return null;
  return (
    <div className="overflow-hidden rounded-lg border" data-testid="asset-shared-with">
      <button
        type="button"
        aria-expanded={open}
        onClick={() => setOpen(!open)}
        className="flex min-h-11 w-full items-center gap-2 bg-muted/40 px-3 text-left text-sm"
      >
        {open ? <ChevronDown aria-hidden="true" className="size-3.5" /> : <ChevronRight aria-hidden="true" className="size-3.5" />}
        <span className="flex-1 font-medium">Shared with {data.total}</span>
        <span className="text-xs text-muted-foreground">{breakdown(data)}</span>
      </button>
      {open && <SharedWithList data={data} />}
    </div>
  );
}

/** matches reports whether a person or persona answers the filter. */
function matches(q: string, ...texts: (string | undefined)[]): boolean {
  return !q || texts.some((t) => (t ?? "").toLowerCase().includes(q));
}

function SharedWithList({ data }: { data: AssetShareSummary }) {
  const [filter, setFilter] = useState("");
  const [all, setAll] = useState(false);
  const names = useDirectoryNames([...data.people.map((p) => p.email), ...data.links.map((l) => l.created_by)]);
  const nameOf = (email: string) => names[email.toLowerCase()] || email;
  const q = filter.trim().toLowerCase();
  const people = data.people.filter((p) => matches(q, p.email, names[p.email.toLowerCase()]));
  const personas = data.personas.filter((p) => matches(q, p.persona));
  const shownPeople = all ? people : people.slice(0, SHOWN);
  return (
    <div className="max-h-96 space-y-2 overflow-auto border-t p-3 text-sm">
      {data.total > SHOWN && (
        <label className="block">
          <span className="sr-only">Filter people</span>
          <Input type="search" className="h-9" placeholder="Filter by name or address" value={filter} onChange={(e) => setFilter(e.target.value)} />
        </label>
      )}
      {personas.length > 0 && <Group label="Personas" />}
      {personas.map((p) => (
        <Row key={`persona-${p.persona}`} name={p.persona} detail={PERMISSION_LABEL[p.permission]} />
      ))}
      {people.length > 0 && <Group label="People" />}
      {shownPeople.map((p) => (
        <Row key={`person-${p.email}`} name={nameOf(p.email)} title={p.email} detail={personDetail(p)} />
      ))}
      {!all && people.length > SHOWN && (
        <button type="button" className="text-xs text-primary hover:underline" onClick={() => setAll(true)}>
          Show {people.length - SHOWN} more
        </button>
      )}
      {!q && <LinkRows links={data.links} nameOf={nameOf} />}
    </div>
  );
}

function personDetail(p: AssetShareSummary["people"][number]): string {
  const expiry = p.expires_at ? ` · expires ${new Date(p.expires_at).toLocaleDateString()}` : "";
  return PERMISSION_LABEL[p.permission] + expiry;
}

function LinkRows({ links, nameOf }: { links: AssetShareSummary["links"]; nameOf: (email: string) => string }) {
  if (links.length === 0) return null;
  return (
    <>
      <Group label="Links" />
      {links.map((l, i) => (
        <Row
          key={`link-${i}`}
          name={l.posted_to ? `${LINK_LABEL[l.access_mode]} posted to ${l.posted_to}` : LINK_LABEL[l.access_mode]}
          detail={`Created by ${nameOf(l.created_by)} · ${l.opened === 1 ? "1 open" : `${l.opened} opens`}`}
        />
      ))}
      <p className="pt-1 text-xs text-muted-foreground">Link addresses are shown only to the owner, in Share.</p>
    </>
  );
}

function Group({ label }: { label: string }) {
  return <span className="block pt-1 text-xs font-medium text-muted-foreground">{label}</span>;
}

function Row({ name, detail, title }: { name: string; detail: string; title?: string }) {
  return (
    <div className="flex justify-between gap-3">
      <span className="min-w-0 truncate" title={title}>
        {name}
      </span>
      <span className="shrink-0 text-xs text-muted-foreground">{detail}</span>
    </div>
  );
}
