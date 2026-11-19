import { useQuery } from "@tanstack/react-query";
import { apiFetch } from "@/api/portal/client";
import { useFavorites, useRecents, type LibraryItem } from "@/api/portal/hooks/library";
import { usePractitionerWorklist, useSMEWorklist } from "@/api/portal/hooks/threads";
import type { ThreadWithMeta } from "@/api/portal/feedback";
import type { Asset, PaginatedResponse } from "@/api/portal/types";
import { formatRelative } from "@/components/feedback/meta";
import { usePortalAccess } from "@/hooks/usePortalAccess";
import { SharedSitesSection } from "./SharedSitesSection";

interface Props {
  onNavigate: (path: string) => void;
}

/** The path that opens a library entry. */
export function itemPath(item: { kind: string; id: string }): string {
  return item.kind === "collection" ? `/collections/${item.id}` : `/assets/${item.id}`;
}

/**
 * HomePage is the portal's landing page (#2504): what the reader opened, what
 * they starred, their drafts, and what is waiting on them. Assets keeps its
 * own page at /assets. Every list is read under the reader's own access, so an
 * item they can no longer open is simply absent.
 */
export function HomePage({ onNavigate }: Props) {
  const recents = useRecents(8);
  const favorites = useFavorites(8);

  const practitioner = usePractitionerWorklist();
  const sme = useSMEWorklist();
  const waiting = [...(practitioner.data?.data ?? []), ...(sme.data?.data ?? [])].slice(0, 5);

  return (
    <div className="space-y-5">
      <div className="space-y-1">
        <h1 className="text-2xl font-semibold">Home</h1>
        <p className="text-sm text-muted-foreground">
          What you opened, what you starred, the sites shared with you, and what is waiting on you.
        </p>
      </div>
      <SharedSitesSection />
      <div className="grid gap-4 md:grid-cols-2">
        <HomeSection
          title="Recent"
          note="Newest first"
          empty="Nothing opened yet. Assets and collections you open appear here."
          loading={recents.isLoading}
          items={(recents.data ?? []).map((it) => ({
            key: `${it.kind}:${it.id}`,
            label: it.name,
            meta: `${it.kind === "collection" ? "Collection · " : ""}${formatRelative(it.at)}`,
            path: itemPath(it),
          }))}
          onNavigate={onNavigate}
        />
        <HomeSection
          title="Favorites"
          empty="Star an asset or collection to keep it here and in the sidebar."
          loading={favorites.isLoading}
          items={(favorites.data ?? []).map((it: LibraryItem) => ({
            key: `${it.kind}:${it.id}`,
            label: it.name,
            meta: it.kind === "collection" ? "Collection" : "Asset",
            path: itemPath(it),
          }))}
          onNavigate={onNavigate}
        />
        <DraftsSection onNavigate={onNavigate} />
        <HomeSection
          title="Waiting on you"
          link={{ label: "Inbox", path: "/feedback" }}
          empty="Nothing is waiting on you."
          loading={practitioner.isLoading || sme.isLoading}
          items={waiting.map((t: ThreadWithMeta) => ({
            key: t.id,
            label: t.target_label || t.title || t.first_message || "Feedback",
            meta: t.first_message ?? "",
            path: threadPath(t),
          }))}
          onNavigate={onNavigate}
        />
      </div>
    </div>
  );
}

/**
 * DraftsSection is the reader's Draft assets. A reader whose assets level is
 * shared owns none, so it could only ever be empty for them, an external
 * person above all, and is left out (#2724).
 */
function DraftsSection({ onNavigate }: Props) {
  const ownsAssets = usePortalAccess().assets !== "shared";
  const drafts = useQuery({
    queryKey: ["home", "drafts"],
    queryFn: () => apiFetch<PaginatedResponse<Asset>>("/assets?scope=mine&status=draft&limit=5"),
    enabled: ownsAssets,
  });
  if (!ownsAssets) return null;
  return (
    <HomeSection
      title="My drafts"
      link={{ label: "All drafts in Assets", path: "/assets" }}
      empty="No drafts. Set an asset's status to Draft to list it here."
      loading={drafts.isLoading}
      items={(drafts.data?.data ?? []).map((a) => ({ key: a.id, label: a.name, badge: "Draft", path: `/assets/${a.id}` }))}
      onNavigate={onNavigate}
    />
  );
}

/** Where a waiting thread is answered: on its asset or collection, else the inbox. */
function threadPath(t: ThreadWithMeta): string {
  if (t.asset_id) return `/assets/${t.asset_id}`;
  if (t.collection_id) return `/collections/${t.collection_id}`;
  return "/feedback";
}

interface Row {
  key: string;
  label: string;
  meta?: string;
  badge?: string;
  path: string;
}

function HomeSection({
  title,
  note,
  link,
  empty,
  loading,
  items,
  onNavigate,
}: {
  title: string;
  note?: string;
  link?: { label: string; path: string };
  empty: string;
  loading: boolean;
  items: Row[];
  onNavigate: (path: string) => void;
}) {
  return (
    <section aria-label={title} className="flex flex-col gap-2 rounded-xl border bg-card p-4">
      <div className="flex items-baseline justify-between gap-2">
        <h2 className="text-base font-semibold">{title}</h2>
        {note && <span className="text-xs text-muted-foreground">{note}</span>}
        {link && (
          <button type="button" className="text-sm text-primary hover:underline" onClick={() => onNavigate(link.path)}>
            {link.label}
          </button>
        )}
      </div>
      {!loading && items.length === 0 && (
        <p className="rounded-lg border border-dashed px-3 py-4 text-sm text-muted-foreground">{empty}</p>
      )}
      <ul className="flex flex-col">
        {items.map((r) => (
          <li key={r.key} className="border-b last:border-b-0">
            <button
              type="button"
              onClick={() => onNavigate(r.path)}
              className="flex min-h-11 w-full items-center justify-between gap-3 py-2 text-left text-sm hover:text-primary"
            >
              <span className="min-w-0 truncate">{r.label}</span>
              {r.badge ? (
                <span className="shrink-0 rounded-full bg-muted px-2 py-0.5 text-xs text-muted-foreground">{r.badge}</span>
              ) : (
                <span className="max-w-[50%] shrink-0 truncate text-xs text-muted-foreground">{r.meta}</span>
              )}
            </button>
          </li>
        ))}
      </ul>
    </section>
  );
}
