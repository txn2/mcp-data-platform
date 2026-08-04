import { useState } from "react";
import { ArrowLeft, Boxes, X } from "lucide-react";
import {
  useDomainMembers,
  useDeleteDomain,
  useUpdateDomain,
  useCatalogSearch,
  DOMAIN_MEMBER_LIMIT,
  MIN_SEARCH_LEN,
  type EntityRef,
  type TableSearchResult,
} from "@/api/portal/datahub";
import { useDebounced } from "@/lib/useDebounced";
import { ListSkeleton, MutationError } from "./catalog/primitives";
import { shortUrn } from "./catalog/utils";
import {
  DeleteControl,
  EntityDescription,
  PageCapNotice,
  TableLink,
  type Usage,
} from "./catalog/governance";

// NO_MEMBERS is the one wording for "nothing is in this domain", shared by the
// membership list and the delete confirmation so the two never disagree.
const NO_MEMBERS = "No table in this connection is in this domain.";

/**
 * DomainDetail is one domain: what it means, which tables are in it, and the
 * edits a curator can make from here. Membership is edited in the direction the
 * per-table entity editor cannot offer — from the domain, over its tables —
 * through the same `catalog/entity/domain` write aimed at each table.
 */
export function DomainDetail({
  conn,
  domain,
  canEdit,
  canDelete,
  onBack,
  onNavigate,
}: {
  conn: string;
  domain: EntityRef;
  canEdit: boolean;
  canDelete: boolean;
  onBack: () => void;
  onNavigate?: (path: string) => void;
}) {
  const membership = useDomainMembers(conn, domain.urn);
  const members = membership.data ?? [];
  const usage: Usage = {
    loading: membership.isLoading,
    failed: membership.isError,
    count: members.length,
  };

  return (
    <div className="space-y-4">
      <button
        onClick={onBack}
        className="inline-flex items-center gap-1.5 text-sm text-muted-foreground hover:text-foreground"
      >
        <ArrowLeft className="h-4 w-4" /> Back to domains
      </button>

      <div>
        <h2 className="flex items-center gap-2 text-lg font-semibold">
          <Boxes className="h-4 w-4 text-muted-foreground" />
          {domain.name || shortUrn(domain.urn)}
        </h2>
        <p className="break-all text-xs text-muted-foreground">{domain.urn}</p>
      </div>

      {canDelete && (
        <DomainDeleteControl conn={conn} domain={domain} usage={usage} onDeleted={onBack} />
      )}

      <EntityDescription conn={conn} entity={domain} canEdit={canEdit} label="Domain description" />

      <DomainMembers
        conn={conn}
        domain={domain}
        members={members}
        state={membership}
        canEdit={canEdit}
        onNavigate={onNavigate}
      />
    </div>
  );
}

// DomainMembers is the membership section: the tables in the domain, and for an
// editor the controls that move one in or out.
//
// It holds ONE domain mutation for the whole section rather than one per row, so
// a rejected write has a single place to be reported. A per-row mutation had
// nowhere to render its error, which made a refused remove look like a remove
// that simply had not refreshed yet.
function DomainMembers({
  conn,
  domain,
  members,
  state,
  canEdit,
  onNavigate,
}: {
  conn: string;
  domain: EntityRef;
  members: TableSearchResult[];
  state: { isLoading: boolean; isError: boolean };
  canEdit: boolean;
  onNavigate?: (path: string) => void;
}) {
  const update = useUpdateDomain(conn);

  return (
    <section className="space-y-2">
      <h3 className="text-sm font-medium">Tables in this domain</h3>
      {state.isError ? (
        <p className="text-sm text-destructive">Failed to load the tables in this domain.</p>
      ) : state.isLoading ? (
        <ListSkeleton />
      ) : members.length === 0 ? (
        <p className="rounded-md border border-dashed px-4 py-6 text-center text-sm text-muted-foreground">
          {NO_MEMBERS}
        </p>
      ) : (
        <>
          <PageCapNotice
            shown={members.length}
            limit={DOMAIN_MEMBER_LIMIT}
            what="tables"
            hint="Search the Tables tab by domain to see the rest."
          />
          <ul className="space-y-2">
            {members.map((t) => (
              <li key={t.urn}>
                <TableLink
                  table={t}
                  onNavigate={onNavigate}
                  trailing={
                    canEdit ? (
                      <RemoveMember
                        table={t}
                        pending={update.isPending}
                        // The write targets the table, not the domain: DataHub
                        // stores the domain on the table, so removing from here
                        // and clearing it in the table's own editor are one
                        // operation.
                        onRemove={() => update.mutate({ urn: t.urn, clear_domain: true })}
                      />
                    ) : undefined
                  }
                />
              </li>
            ))}
          </ul>
        </>
      )}
      {canEdit && (
        <AddMember
          conn={conn}
          memberURNs={members.map((t) => t.urn)}
          pending={update.isPending}
          onAdd={(urn, done) => update.mutate({ urn, domain: domain.urn }, { onSuccess: done })}
        />
      )}
      <MutationError mut={update} />
    </section>
  );
}

// DomainDeleteControl retires a domain definition behind the shared
// confirmation, supplying the impact sentence specific to a domain.
function DomainDeleteControl({
  conn,
  domain,
  usage,
  onDeleted,
}: {
  conn: string;
  domain: EntityRef;
  usage: Usage;
  onDeleted: () => void;
}) {
  const del = useDeleteDomain(conn);
  return (
    <DeleteControl
      label="Delete domain"
      impact={<DeleteImpact usage={usage} />}
      mut={del}
      onConfirm={() => del.mutate(domain.urn, { onSuccess: onDeleted })}
    />
  );
}

// DeleteImpact states what the delete will affect, in each state the membership
// read can be in. A failed read says so: reporting "nothing is in this domain"
// from a read that never answered would understate the delete. The delete itself
// is one upstream mutation against the domain entity and touches no table, which
// is why the tables are described as left behind rather than as deleted.
function DeleteImpact({ usage }: { usage: Usage }) {
  if (usage.loading) return <>Checking what is in this domain…</>;
  if (usage.failed) {
    return <>Could not check what is in this domain, so the effect of deleting it is unknown.</>;
  }
  if (usage.count === 0) return <>{NO_MEMBERS}</>;
  // The membership read is one page, so a full page is a floor, not a count.
  const atCap = usage.count >= DOMAIN_MEMBER_LIMIT;
  const held =
    usage.count === 1
      ? "1 table in this connection is in this domain."
      : `${atCap ? "At least " : ""}${usage.count} tables in this connection are in this domain.`;
  return <>{held} Deleting removes the domain definition from DataHub and leaves those tables without a domain.</>;
}

// RemoveMember is the per-row control that takes one table out of the domain.
// The write itself belongs to DomainMembers, which owns the one mutation the
// section reports through.
function RemoveMember({
  table,
  pending,
  onRemove,
}: {
  table: TableSearchResult;
  pending: boolean;
  onRemove: () => void;
}) {
  return (
    <button
      onClick={onRemove}
      disabled={pending}
      aria-label={`Remove ${table.name || shortUrn(table.urn)} from this domain`}
      className="inline-flex items-center gap-1 rounded-md border px-2 py-1 text-xs text-destructive hover:bg-destructive/10 disabled:opacity-50"
    >
      <X className="h-3 w-3" /> Remove
    </button>
  );
}

// AddMember searches the catalog and puts the chosen table in this domain.
// DataHub gives a table at most one domain, so adding a table that already has
// one moves it rather than adding a second; the form says so instead of leaving
// the curator to discover it.
function AddMember({
  conn,
  memberURNs,
  pending,
  onAdd,
}: {
  conn: string;
  memberURNs: string[];
  pending: boolean;
  onAdd: (tableURN: string, done: () => void) => void;
}) {
  const [query, setQuery] = useState("");
  const debounced = useDebounced(query, 250);
  const results = useCatalogSearch(conn, debounced);
  const already = new Set(memberURNs);
  const candidates = (results.data ?? []).filter((t) => !already.has(t.urn));

  return (
    <div className="space-y-2 rounded-lg border border-dashed p-3">
      <label className="block space-y-1">
        <span className="text-sm font-medium">Add a table to this domain</span>
        <input
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="Search tables by name…"
          className="w-full rounded-md border bg-background px-2 py-1.5 text-sm outline-none ring-ring focus:ring-2"
        />
      </label>
      <p className="text-xs text-muted-foreground">
        A table has at most one domain, so adding one that is already in another domain moves it
        here.
      </p>

      {debounced.trim().length >= MIN_SEARCH_LEN &&
        (results.isError ? (
          <p className="text-sm text-destructive">Table search failed.</p>
        ) : results.isLoading ? (
          <p className="text-xs text-muted-foreground">Searching…</p>
        ) : candidates.length === 0 ? (
          <p className="text-xs text-muted-foreground">
            No table matches that name outside this domain.
          </p>
        ) : (
          <ul className="space-y-1">
            {candidates.map((t) => (
              <li key={t.urn} className="flex items-center justify-between gap-2 rounded-md border px-2 py-1.5">
                <span className="truncate text-sm">{t.name || shortUrn(t.urn)}</span>
                <button
                  onClick={() => onAdd(t.urn, () => setQuery(""))}
                  disabled={pending}
                  className="shrink-0 rounded-md border px-2 py-1 text-xs font-medium hover:bg-muted disabled:opacity-50"
                >
                  Add
                </button>
              </li>
            ))}
          </ul>
        ))}
    </div>
  );
}
