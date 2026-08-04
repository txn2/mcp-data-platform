import { useState } from "react";
import { ArrowLeft, Search, Plus, Boxes } from "lucide-react";
import {
  useDomainList,
  useCreateDomain,
  DOMAIN_LIST_LIMIT,
  type EntityRef,
} from "@/api/portal/datahub";
import { useConnectionWritable } from "@/components/knowledge/DataHubConnectionSelect";
import { useAuthStore } from "@/stores/auth";
import { ListSkeleton, MutationError } from "./catalog/primitives";
import { clearURNFromLocation, deepLinkedURN, filterDomains } from "./catalog/utils";
import { DeepLinkedEntry, PageCapNotice, VocabCard } from "./catalog/governance";
import { DomainDetail } from "./DomainDetail";

/**
 * DomainsTab is the Domains tab of the Catalog section (#1157, #1194): the
 * business areas the catalog is grouped into, rather than the domain carried by
 * one table. It lists and name-filters the domains on a DataHub connection,
 * opens one to show its description and the tables in it, and — when the persona
 * grants the matching datahub tool and the connection is write-enabled — creates
 * a domain, edits its description, retires it, and moves tables in and out. The
 * API enforces the same gates. The connection is chosen by CatalogSection, which
 * remounts this on a change so an open domain never outlives the connection it
 * belongs to.
 */
export function DomainsTab({
  conn,
  onNavigate,
}: {
  conn: string;
  onNavigate?: (path: string) => void;
}) {
  const [mode, setMode] = useState<"list" | "create">("list");
  const [selected, setSelected] = useState<EntityRef | null>(null);
  // linked is the domain a `?urn=` deep link addresses (#1159), carried as a URN
  // because the link has no name to carry, and cleared from the URL on the way
  // back so a refresh does not reopen what the reader just left.
  const [linked, setLinked] = useState<string | null>(() => deepLinkedURN("domains"));
  const writable = useConnectionWritable(conn);
  const tools = useAuthStore((s) => s.user?.tools);
  const isAdmin = useAuthStore((s) => s.isAdmin());
  const has = (t: string) => (tools?.includes(t) ?? false) || isAdmin;
  const canCreate = writable && has("datahub_create");
  const canEdit = writable && has("datahub_update");
  const canDelete = writable && has("datahub_delete");

  const back = () => {
    setSelected(null);
    setLinked(null);
    clearURNFromLocation();
    setMode("list");
  };

  const detail = (domain: EntityRef) => (
    <DomainDetail
      key={domain.urn}
      conn={conn}
      domain={domain}
      canEdit={canEdit}
      canDelete={canDelete}
      onBack={back}
      onNavigate={onNavigate}
    />
  );

  return (
    <div className="space-y-4">
      {selected ? (
        detail(selected)
      ) : linked ? (
        <LinkedDomain conn={conn} urn={linked} onBack={back}>
          {detail}
        </LinkedDomain>
      ) : mode === "create" ? (
        <DomainForm conn={conn} onDone={back} />
      ) : (
        <DomainList
          conn={conn}
          canCreate={canCreate}
          onOpen={setSelected}
          onCreate={() => setMode("create")}
        />
      )}
    </div>
  );
}

// LinkedDomain resolves a deep-linked domain URN against this connection's
// domain list, which is the only read DataHub offers for a domain: there is no
// fetch-by-URN, and the list itself is capped upstream at 100.
function LinkedDomain({
  conn,
  urn,
  onBack,
  children,
}: {
  conn: string;
  urn: string;
  onBack: () => void;
  children: (domain: EntityRef) => React.ReactNode;
}) {
  const { data, isLoading, isError } = useDomainList(conn);
  return (
    <DeepLinkedEntry
      urn={urn}
      entries={data}
      isLoading={isLoading}
      isError={isError}
      what="domain"
      backLabel="Back to domains"
      onBack={onBack}
    >
      {children}
    </DeepLinkedEntry>
  );
}

function DomainList({
  conn,
  canCreate,
  onOpen,
  onCreate,
}: {
  conn: string;
  canCreate: boolean;
  onOpen: (domain: EntityRef) => void;
  onCreate: () => void;
}) {
  const [query, setQuery] = useState("");
  const { data, isLoading, isError } = useDomainList(conn);
  // DataHub has no name-scoped domain search: the list read returns the whole
  // (capped) set, so filtering is client-side and needs no debounce. That is
  // also why an empty result reads differently here than in the Tags tab —
  // nothing was refetched.
  const domains = filterDomains(data ?? [], query);

  return (
    <div className="space-y-4">
      <div className="flex items-center gap-2">
        <div className="relative flex-1">
          <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
          <input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Filter domains by name…"
            className="w-full rounded-md border bg-background py-2 pl-9 pr-3 text-sm outline-none ring-ring focus:ring-2"
          />
        </div>
        {canCreate && (
          <button
            onClick={onCreate}
            className="inline-flex items-center gap-1.5 rounded-md bg-primary px-3 py-2 text-sm font-medium text-primary-foreground hover:bg-primary/90"
          >
            <Plus className="h-4 w-4" /> New domain
          </button>
        )}
      </div>

      {isError ? (
        <p className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">
          Failed to load domains.
        </p>
      ) : isLoading ? (
        <ListSkeleton />
      ) : domains.length === 0 ? (
        <p className="rounded-md border border-dashed px-4 py-8 text-center text-sm text-muted-foreground">
          {query.trim()
            ? "No domains match that name."
            : "This connection has no domains yet."}
        </p>
      ) : (
        <>
          {/* The cap is upstream's, not this surface's: DataHub's listDomains
              query asks for 100 and the lookup route takes no limit, so a full
              list means there are domains this page cannot reach at all. */}
          <PageCapNotice
            shown={data?.length ?? 0}
            limit={DOMAIN_LIST_LIMIT}
            what="domains"
            hint="DataHub caps this list; the rest are reachable in the DataHub UI."
          />
          <ul className="grid gap-2 sm:grid-cols-2">
            {domains.map((d) => (
              <li key={d.urn}>
                <VocabCard entry={d} icon={Boxes} onOpen={() => onOpen(d)} />
              </li>
            ))}
          </ul>
        </>
      )}
    </div>
  );
}

function DomainForm({ conn, onDone }: { conn: string; onDone: () => void }) {
  const create = useCreateDomain(conn);
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");

  return (
    <div className="space-y-4">
      <button
        onClick={onDone}
        className="inline-flex items-center gap-1.5 text-sm text-muted-foreground hover:text-foreground"
      >
        <ArrowLeft className="h-4 w-4" /> Cancel
      </button>
      <h2 className="text-lg font-semibold">New domain</h2>

      <label className="block space-y-1">
        <span className="text-sm font-medium">Name</span>
        <input
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="e.g. Finance"
          className="w-full rounded-md border bg-background px-2 py-1.5 text-sm outline-none ring-ring focus:ring-2"
        />
      </label>

      <label className="block space-y-1">
        <span className="text-sm font-medium">Description</span>
        <textarea
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          rows={3}
          placeholder="What this domain covers, and which teams own it."
          className="w-full rounded-md border bg-background px-2 py-1.5 text-sm outline-none ring-ring focus:ring-2"
        />
      </label>

      <p className="text-xs text-muted-foreground">
        DataHub indexes new domains asynchronously, so a domain you create may take a moment to
        appear in the list.
      </p>

      <MutationError mut={create} />

      <div className="flex gap-2">
        <button
          onClick={() =>
            create.mutate(
              { name: name.trim(), description: description.trim() || undefined },
              { onSuccess: onDone },
            )
          }
          disabled={name.trim() === "" || create.isPending}
          className="rounded-md bg-primary px-4 py-1.5 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
        >
          Create domain
        </button>
        <button onClick={onDone} className="rounded-md border px-4 py-1.5 text-sm hover:bg-muted">
          Cancel
        </button>
      </div>
    </div>
  );
}
