import { useState } from "react";
import {
  ArrowLeft,
  Search,
  Tag,
  Users,
  BookMarked,
  Building2,
  Pencil,
  X,
  Plus,
  AlertCircle,
} from "lucide-react";
import {
  useCatalogBrowse,
  useCatalogSearch,
  useCatalogEntity,
  useUpdateDescription,
  useUpdateTags,
  useUpdateOwners,
  useUpdateGlossaryTerms,
  useUpdateDomain,
  useTagLookup,
  useGlossaryLookup,
  useDomainLookup,
  MIN_SEARCH_LEN,
  type TableSearchResult,
  type CatalogEntity,
  type EntityRef,
} from "@/api/portal/datahub";
import { DataHubConnectionSelect, useConnectionWritable } from "@/components/knowledge/DataHubConnectionSelect";
import { useAuthStore } from "@/stores/auth";
import { useDebounced } from "@/lib/useDebounced";
import { ApiError } from "@/api/portal/client";

const MIN_SEARCH = MIN_SEARCH_LEN;

/**
 * CatalogTab is the Knowledge > Catalog sub-tab (#719): browse/search DataHub
 * datasets and view/edit their metadata. Editing (description, tags, owners,
 * glossary terms, domain) is shown only when the persona grants datahub_update
 * and the selected connection is write-enabled; the API enforces the same.
 */
export function CatalogTab({ conn, onConnChange }: { conn: string; onConnChange: (c: string) => void }) {
  const [urn, setUrn] = useState<string | null>(null);
  const writable = useConnectionWritable(conn);
  const hasWriteTool = useAuthStore(
    (s) => (s.user?.tools?.includes("datahub_update") ?? false) || s.isAdmin(),
  );
  const canEdit = writable && hasWriteTool;

  return (
    <div className="space-y-4">
      <DataHubConnectionSelect value={conn} onChange={onConnChange} />
      {!conn ? null : urn ? (
        <CatalogEntityDetail conn={conn} urn={urn} canEdit={canEdit} onBack={() => setUrn(null)} />
      ) : (
        <CatalogList conn={conn} onOpen={setUrn} />
      )}
    </div>
  );
}

function CatalogList({ conn, onOpen }: { conn: string; onOpen: (urn: string) => void }) {
  const [query, setQuery] = useState("");
  const debounced = useDebounced(query, 250);
  const searching = debounced.trim().length >= MIN_SEARCH;
  const browse = useCatalogBrowse(conn, { limit: 50 });
  const search = useCatalogSearch(conn, debounced, { limit: 25 });
  const active = searching ? search : browse;
  const results: TableSearchResult[] = active.data ?? [];

  return (
    <div className="space-y-4">
      <div className="relative">
        <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
        <input
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="Search datasets by name, description, or tag…"
          className="w-full rounded-md border bg-background py-2 pl-9 pr-3 text-sm outline-none ring-ring focus:ring-2"
        />
      </div>

      {active.isError ? (
        <p className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">
          Failed to load the catalog.
        </p>
      ) : active.isLoading ? (
        <ListSkeleton />
      ) : results.length === 0 ? (
        <p className="rounded-md border border-dashed px-4 py-8 text-center text-sm text-muted-foreground">
          {searching ? "No datasets match your search." : "No datasets found in this connection."}
        </p>
      ) : (
        <ul className="grid gap-2 sm:grid-cols-2">
          {results.map((r) => (
            <li key={r.urn}>
              <button
                onClick={() => onOpen(r.urn)}
                className="flex w-full flex-col gap-1 rounded-lg border p-3 text-left transition-colors hover:border-primary/50 hover:bg-muted/50"
              >
                <span className="truncate text-sm font-medium">{r.name || r.urn}</span>
                {r.description && (
                  <span className="line-clamp-2 text-xs text-muted-foreground">{r.description}</span>
                )}
                <span className="mt-1 flex flex-wrap items-center gap-1.5">
                  {r.platform && (
                    <span className="rounded bg-muted px-1.5 py-0.5 text-[11px] text-muted-foreground">
                      {r.platform}
                    </span>
                  )}
                  {(r.tags ?? []).slice(0, 4).map((t) => (
                    <span key={t} className="rounded bg-primary/10 px-1.5 py-0.5 text-[11px] text-primary">
                      {shortUrn(t)}
                    </span>
                  ))}
                </span>
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function CatalogEntityDetail({
  conn,
  urn,
  canEdit,
  onBack,
}: {
  conn: string;
  urn: string;
  canEdit: boolean;
  onBack: () => void;
}) {
  const { data, isLoading, isError } = useCatalogEntity(conn, urn);

  return (
    <div className="space-y-4">
      <button
        onClick={onBack}
        className="inline-flex items-center gap-1.5 text-sm text-muted-foreground hover:text-foreground"
      >
        <ArrowLeft className="h-4 w-4" /> Back to catalog
      </button>

      {isError || !data ? (
        isLoading ? (
          <ListSkeleton />
        ) : (
          <p className="text-sm text-destructive">Failed to load this entity.</p>
        )
      ) : (
        <EntityBody conn={conn} entity={data} canEdit={canEdit} />
      )}
    </div>
  );
}

function EntityBody({ conn, entity, canEdit }: { conn: string; entity: CatalogEntity; canEdit: boolean }) {
  const ctx = entity.context ?? {};
  const columns = Object.values(entity.columns ?? {});
  return (
    <div className="space-y-6">
      <div>
        <h2 className="break-all text-lg font-semibold">{ctx.urn ?? entity.urn}</h2>
      </div>

      <DescriptionEditor conn={conn} urn={entity.urn} value={ctx.description ?? ""} canEdit={canEdit} />

      <ChipSetSection
        icon={<Tag className="h-4 w-4" />}
        title="Tags"
        conn={conn}
        urn={entity.urn}
        values={(ctx.tag_refs ?? []).map((t) => ({ key: t.urn, label: t.name || shortUrn(t.urn) }))}
        canEdit={canEdit}
        kind="tags"
        placeholder="Search tags by name…"
      />

      <ChipSetSection
        icon={<BookMarked className="h-4 w-4" />}
        title="Glossary terms"
        conn={conn}
        urn={entity.urn}
        values={(ctx.glossary_terms ?? []).map((g) => ({ key: g.urn, label: g.name || shortUrn(g.urn) }))}
        canEdit={canEdit}
        kind="glossary"
        placeholder="Search glossary terms by name…"
      />

      <OwnersSection conn={conn} urn={entity.urn} owners={ctx.owners ?? []} canEdit={canEdit} />

      <DomainSection conn={conn} urn={entity.urn} domain={ctx.domain ?? null} canEdit={canEdit} />

      {columns.length > 0 && (
        <section>
          <h3 className="mb-2 text-sm font-semibold">Columns</h3>
          <div className="overflow-hidden rounded-lg border">
            <table className="w-full text-sm">
              <thead className="bg-muted/50 text-left text-xs text-muted-foreground">
                <tr>
                  <th className="px-3 py-2 font-medium">Name</th>
                  <th className="px-3 py-2 font-medium">Description</th>
                  <th className="px-3 py-2 font-medium">Classification</th>
                </tr>
              </thead>
              <tbody>
                {columns.map((c) => (
                  <tr key={c.name} className="border-t align-top">
                    <td className="px-3 py-2 font-mono text-xs">{c.name}</td>
                    <td className="px-3 py-2 text-muted-foreground">{c.description || "—"}</td>
                    <td className="px-3 py-2">
                      <span className="flex flex-wrap gap-1">
                        {c.is_pii && <Badge tone="amber">PII</Badge>}
                        {c.is_sensitive && <Badge tone="amber">Sensitive</Badge>}
                        {(c.tags ?? []).map((t) => (
                          <Badge key={t} tone="primary">
                            {shortUrn(t)}
                          </Badge>
                        ))}
                      </span>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>
      )}
    </div>
  );
}

function DescriptionEditor({
  conn,
  urn,
  value,
  canEdit,
}: {
  conn: string;
  urn: string;
  value: string;
  canEdit: boolean;
}) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(value);
  const mut = useUpdateDescription(conn);

  return (
    <section>
      <div className="mb-2 flex items-center justify-between">
        <h3 className="text-sm font-semibold">Description</h3>
        {canEdit && !editing && (
          <EditButton
            onClick={() => {
              setDraft(value);
              setEditing(true);
            }}
          />
        )}
      </div>
      {editing ? (
        <div className="space-y-2">
          <textarea
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            rows={4}
            className="w-full rounded-md border bg-background p-2 text-sm outline-none ring-ring focus:ring-2"
          />
          <MutationError mut={mut} />
          <div className="flex gap-2">
            <SaveButton
              disabled={mut.isPending}
              onClick={() =>
                mut.mutate({ urn, description: draft }, { onSuccess: () => setEditing(false) })
              }
            />
            <CancelButton onClick={() => setEditing(false)} />
          </div>
        </div>
      ) : (
        <p className="whitespace-pre-wrap text-sm text-muted-foreground">{value || "No description."}</p>
      )}
    </section>
  );
}

type ChipKind = "tags" | "glossary";

function ChipSetSection({
  icon,
  title,
  conn,
  urn,
  values,
  canEdit,
  kind,
  placeholder,
}: {
  icon: React.ReactNode;
  title: string;
  conn: string;
  urn: string;
  values: { key: string; label: string }[];
  canEdit: boolean;
  kind: ChipKind;
  placeholder: string;
}) {
  const [query, setQuery] = useState("");
  const tags = useUpdateTags(conn);
  const glossary = useUpdateGlossaryTerms(conn);
  const mut = kind === "tags" ? tags : glossary;

  // Only the active kind's lookup runs; the other gets an empty query (disabled).
  const debounced = useDebounced(query, 250);
  const tagLookup = useTagLookup(conn, kind === "tags" ? debounced : "");
  const glossaryLookup = useGlossaryLookup(conn, kind === "glossary" ? debounced : "");
  const lookup = kind === "tags" ? tagLookup : glossaryLookup;
  const existing = new Set(values.map((v) => v.key));

  // Power-user fallback: an exact, well-formed URN typed into the box is offered as
  // a candidate so a value that name search does not surface (e.g. a brand-new tag)
  // can still be applied without a raw free-text field (#785 review).
  const urnType = kind === "tags" ? "tag" : "glossaryTerm";
  const candidates = withRawUrn(lookup.data ?? [], query, urnType);

  const pick = (ref: EntityRef) => {
    if (existing.has(ref.urn)) return;
    mut.mutate({ urn, add: [ref.urn] }, { onSuccess: () => setQuery("") });
  };

  return (
    <section>
      <h3 className="mb-2 flex items-center gap-1.5 text-sm font-semibold">
        {icon} {title}
      </h3>
      <div className="flex flex-wrap items-center gap-1.5">
        {values.length === 0 && <span className="text-sm text-muted-foreground">None.</span>}
        {values.map((v) => (
          <span
            key={v.key}
            className="inline-flex items-center gap-1 rounded-full bg-primary/10 px-2 py-0.5 text-xs text-primary"
          >
            {v.label}
            {canEdit && (
              <button
                aria-label={`Remove ${v.label}`}
                onClick={() => mut.mutate({ urn, remove: [v.key] })}
                className="rounded-full hover:bg-primary/20"
              >
                <X className="h-3 w-3" />
              </button>
            )}
          </span>
        ))}
      </div>
      {canEdit && (
        <MetadataPicker
          placeholder={placeholder}
          query={query}
          setQuery={setQuery}
          candidates={candidates}
          loading={lookup.isFetching || (lookup.data === undefined && !lookup.isError)}
          isPending={mut.isPending}
          existingKeys={existing}
          onPick={pick}
          emptyHint={lookup.isError ? "Lookup failed." : "No matches."}
        />
      )}
      <MutationError mut={mut} />
    </section>
  );
}

function OwnersSection({
  conn,
  urn,
  owners,
  canEdit,
}: {
  conn: string;
  urn: string;
  owners: { urn: string; name?: string; email?: string; type: string }[];
  canEdit: boolean;
}) {
  const [ownerUrn, setOwnerUrn] = useState("");
  const [ownerType, setOwnerType] = useState("TECHNICAL_OWNER");
  const mut = useUpdateOwners(conn);

  return (
    <section>
      <h3 className="mb-2 flex items-center gap-1.5 text-sm font-semibold">
        <Users className="h-4 w-4" /> Owners
      </h3>
      <div className="space-y-1">
        {owners.length === 0 && <span className="text-sm text-muted-foreground">None.</span>}
        {owners.map((o) => (
          <div key={o.urn} className="flex items-center gap-2 text-sm">
            <span>{o.name || o.email || shortUrn(o.urn)}</span>
            <span className="rounded bg-muted px-1.5 py-0.5 text-[11px] text-muted-foreground">{o.type}</span>
            {canEdit && (
              <button
                aria-label={`Remove owner ${o.urn}`}
                onClick={() => mut.mutate({ urn, remove: [o.urn] })}
                className="text-muted-foreground hover:text-destructive"
              >
                <X className="h-3.5 w-3.5" />
              </button>
            )}
          </div>
        ))}
      </div>
      {canEdit && (
        <div className="mt-2 space-y-1">
          <div className="flex flex-wrap items-center gap-2">
            <input
              value={ownerUrn}
              onChange={(e) => setOwnerUrn(e.target.value)}
              placeholder="urn:li:corpuser:alice"
              className="w-64 rounded-md border bg-background px-2 py-1 font-mono text-xs outline-none ring-ring focus:ring-2"
            />
            <select
              value={ownerType}
              onChange={(e) => setOwnerType(e.target.value)}
              className="rounded-md border bg-background px-2 py-1 text-xs outline-none ring-ring focus:ring-2"
            >
              <option>TECHNICAL_OWNER</option>
              <option>BUSINESS_OWNER</option>
              <option>DATA_STEWARD</option>
            </select>
            <AddButton
              disabled={mut.isPending || !ownerUrn.trim()}
              onClick={() =>
                mut.mutate(
                  { urn, add_owners: [{ owner_urn: ownerUrn.trim(), ownership_type: ownerType }] },
                  { onSuccess: () => setOwnerUrn("") },
                )
              }
            />
          </div>
          <p className="text-[11px] text-muted-foreground">
            Enter a DataHub user or group URN, e.g. <code>urn:li:corpuser:alice</code> or{" "}
            <code>urn:li:corpGroup:data-eng</code>.
          </p>
        </div>
      )}
      <MutationError mut={mut} />
    </section>
  );
}

function DomainSection({
  conn,
  urn,
  domain,
  canEdit,
}: {
  conn: string;
  urn: string;
  domain: { urn: string; name: string } | null;
  canEdit: boolean;
}) {
  const [query, setQuery] = useState("");
  const mut = useUpdateDomain(conn);
  // Domains have no name-scoped search upstream, so load the full list and filter
  // client-side. Fetch only when the editor can edit.
  const lookup = useDomainLookup(conn, canEdit);
  // The upstream domain list is capped (100), so a domain beyond it would be
  // unreachable; an exact urn:li:domain URN typed into the box is offered as a
  // fallback candidate so any domain can still be set (#785 review).
  const candidates = withRawUrn(filterDomains(lookup.data ?? [], query), query, "domain");

  const pick = (ref: EntityRef) => {
    mut.mutate({ urn, domain: ref.urn }, { onSuccess: () => setQuery("") });
  };

  return (
    <section>
      <h3 className="mb-2 flex items-center gap-1.5 text-sm font-semibold">
        <Building2 className="h-4 w-4" /> Domain
      </h3>
      <div className="flex items-center gap-2 text-sm">
        {domain ? (
          <>
            <span>{domain.name || shortUrn(domain.urn)}</span>
            {canEdit && (
              <button
                onClick={() => mut.mutate({ urn, clear_domain: true })}
                className="text-xs text-muted-foreground hover:text-destructive"
              >
                Clear
              </button>
            )}
          </>
        ) : (
          <span className="text-muted-foreground">None.</span>
        )}
      </div>
      {canEdit && (
        <MetadataPicker
          placeholder="Search domains by name…"
          query={query}
          setQuery={setQuery}
          candidates={candidates}
          loading={lookup.isFetching || (lookup.data === undefined && !lookup.isError)}
          isPending={mut.isPending}
          existingKeys={new Set(domain ? [domain.urn] : [])}
          onPick={pick}
          openOnFocus
          emptyHint={lookup.isError ? "Failed to load domains." : "No domains match."}
        />
      )}
      <MutationError mut={mut} />
    </section>
  );
}

// filterDomains narrows the full domain list to those whose name or URN contains
// the query (case-insensitive); an empty query returns the whole list so a
// focused picker shows every domain.
function filterDomains(domains: EntityRef[], query: string): EntityRef[] {
  const q = query.trim().toLowerCase();
  if (!q) return domains;
  return domains.filter(
    (d) => d.name.toLowerCase().includes(q) || d.urn.toLowerCase().includes(q),
  );
}

// withRawUrn prepends the typed query as a candidate when it is itself a
// well-formed `urn:li:<type>:<id>` URN not already in the list, so an exact URN a
// user pastes (or a value name search cannot surface) stays applicable.
function withRawUrn(candidates: EntityRef[], query: string, type: string): EntityRef[] {
  const q = query.trim();
  const prefix = `urn:li:${type}:`;
  if (!q.startsWith(prefix) || q.length <= prefix.length) return candidates;
  if (candidates.some((c) => c.urn === q)) return candidates;
  return [{ urn: q, name: q }, ...candidates];
}

// --- small shared bits ---

function shortUrn(urn: string): string {
  const parts = urn.split(":");
  return parts[parts.length - 1] || urn;
}

function Badge({ tone, children }: { tone: "primary" | "amber"; children: React.ReactNode }) {
  const cls =
    tone === "amber"
      ? "bg-amber-500/10 text-amber-600 dark:text-amber-400"
      : "bg-primary/10 text-primary";
  return <span className={`rounded px-1.5 py-0.5 text-[11px] ${cls}`}>{children}</span>;
}

function ListSkeleton() {
  return (
    <div className="grid gap-2 sm:grid-cols-2">
      {Array.from({ length: 6 }).map((_, i) => (
        <div key={i} className="h-16 animate-pulse rounded-lg border bg-muted/40" />
      ))}
    </div>
  );
}

function EditButton({ onClick }: { onClick: () => void }) {
  return (
    <button
      onClick={onClick}
      className="inline-flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground"
    >
      <Pencil className="h-3.5 w-3.5" /> Edit
    </button>
  );
}

function SaveButton({ onClick, disabled }: { onClick: () => void; disabled?: boolean }) {
  return (
    <button
      onClick={onClick}
      disabled={disabled}
      className="rounded-md bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
    >
      Save
    </button>
  );
}

function CancelButton({ onClick }: { onClick: () => void }) {
  return (
    <button onClick={onClick} className="rounded-md border px-3 py-1.5 text-sm hover:bg-muted">
      Cancel
    </button>
  );
}

function AddButton({
  onClick,
  disabled,
  label = "Add",
}: {
  onClick: () => void;
  disabled?: boolean;
  label?: string;
}) {
  return (
    <button
      onClick={onClick}
      disabled={disabled}
      className="inline-flex items-center gap-1 rounded-md border px-2 py-1 text-xs font-medium hover:bg-muted disabled:opacity-50"
    >
      <Plus className="h-3 w-3" /> {label}
    </button>
  );
}

function MutationError({ mut }: { mut: { isError: boolean; error: unknown } }) {
  if (!mut.isError) return null;
  const msg = mut.error instanceof ApiError ? mut.error.detail : "Update failed.";
  return (
    <p
      role="alert"
      className="mt-2 flex items-start gap-1.5 rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive"
    >
      <AlertCircle className="mt-0.5 h-4 w-4 shrink-0" />
      <span>{msg}</span>
    </p>
  );
}

/**
 * MetadataPicker is the shared typeahead used by the tag, glossary, and domain
 * editors (#785): the user types a display name and picks a result that resolves
 * to a URN, so raw URN entry is never required. Selecting a candidate does not
 * blur the input (the list uses onMouseDown preventDefault), and the dropdown
 * opens on focus for a preloaded list (openOnFocus) or once the query reaches the
 * search threshold otherwise.
 */
function MetadataPicker({
  placeholder,
  query,
  setQuery,
  candidates,
  loading,
  isPending,
  existingKeys,
  onPick,
  openOnFocus = false,
  emptyHint = "No matches.",
}: {
  placeholder: string;
  query: string;
  setQuery: (v: string) => void;
  candidates: EntityRef[];
  loading: boolean;
  isPending: boolean;
  existingKeys: Set<string>;
  onPick: (ref: EntityRef) => void;
  openOnFocus?: boolean;
  emptyHint?: string;
}) {
  const [focused, setFocused] = useState(false);
  const open = focused && (openOnFocus || query.trim().length >= MIN_SEARCH);

  return (
    <div className="relative mt-2 w-72">
      <div className="relative">
        <Search className="pointer-events-none absolute left-2 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
        <input
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          onFocus={() => setFocused(true)}
          onBlur={() => setFocused(false)}
          placeholder={placeholder}
          disabled={isPending}
          className="w-full rounded-md border bg-background py-1 pl-8 pr-2 text-xs outline-none ring-ring focus:ring-2 disabled:opacity-50"
        />
      </div>
      {open && (
        <ul
          // Keep focus on the input so a click selects instead of blurring first.
          onMouseDown={(e) => e.preventDefault()}
          className="absolute z-10 mt-1 max-h-56 w-full overflow-y-auto rounded-md border bg-popover text-popover-foreground shadow-md"
        >
          {candidates.length === 0 ? (
            <li className="px-3 py-2 text-xs text-muted-foreground">
              {loading ? "Searching…" : emptyHint}
            </li>
          ) : (
            candidates.map((c) => {
              const already = existingKeys.has(c.urn);
              return (
                <li key={c.urn}>
                  <button
                    type="button"
                    disabled={already || isPending}
                    onClick={() => onPick(c)}
                    className="flex w-full items-center justify-between gap-2 px-3 py-1.5 text-left text-xs hover:bg-muted disabled:opacity-50"
                  >
                    <span className="truncate">
                      <span className="font-medium">{c.name || shortUrn(c.urn)}</span>
                      <span className="ml-1.5 font-mono text-[11px] text-muted-foreground">
                        {shortUrn(c.urn)}
                      </span>
                    </span>
                    {already ? (
                      <span className="shrink-0 text-muted-foreground">added</span>
                    ) : (
                      <Plus className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
                    )}
                  </button>
                </li>
              );
            })
          )}
        </ul>
      )}
    </div>
  );
}
