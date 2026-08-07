import { useState } from "react";
import { ArrowLeft, Search, Plus, Boxes } from "lucide-react";
import {
  useDomainList,
  useCreateDomain,
  DOMAIN_LIST_LIMIT,
  type EntityRef,
} from "@/api/portal/datahub";
import { useConnectionWritable } from "@/components/knowledge/DataHubConnectionSelect";
import { EmptyState } from "@/components/patterns/EmptyState";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { useAuthStore } from "@/stores/auth";
import { CancelButton, ListSkeleton, MutationError } from "./catalog/primitives";
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
          <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Filter domains by name…"
            className="pl-9"
          />
        </div>
        {canCreate && (
          <Button onClick={onCreate}>
            <Plus /> New domain
          </Button>
        )}
      </div>

      {isError ? (
        <Alert variant="destructive">
          <AlertDescription>Failed to load domains.</AlertDescription>
        </Alert>
      ) : isLoading ? (
        <ListSkeleton />
      ) : domains.length === 0 ? (
        <EmptyState>
          {query.trim() ? "No domains match that name." : "This connection has no domains yet."}
        </EmptyState>
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
        className="inline-flex items-center gap-1.5 text-sm text-muted-foreground transition-colors hover:text-foreground"
      >
        <ArrowLeft className="size-4" /> Cancel
      </button>
      <h2 className="text-lg font-semibold">New domain</h2>

      <div className="space-y-1.5">
        <Label htmlFor="domain-name">Name</Label>
        <Input
          id="domain-name"
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="e.g. Finance"
        />
      </div>

      <div className="space-y-1.5">
        <Label htmlFor="domain-description">Description</Label>
        <Textarea
          id="domain-description"
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          rows={3}
          placeholder="What this domain covers, and which teams own it."
        />
      </div>

      <p className="text-xs text-muted-foreground">
        DataHub indexes new domains asynchronously, so a domain you create may take a moment to
        appear in the list.
      </p>

      <MutationError mut={create} />

      <div className="flex gap-2">
        <Button
          onClick={() =>
            create.mutate(
              { name: name.trim(), description: description.trim() || undefined },
              { onSuccess: onDone },
            )
          }
          disabled={name.trim() === "" || create.isPending}
        >
          Create domain
        </Button>
        <CancelButton onClick={onDone} />
      </div>
    </div>
  );
}
