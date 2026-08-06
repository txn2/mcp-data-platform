import { ArrowLeft, BookMarked, ChevronRight, FileText, Library } from "lucide-react";
import {
  useGlossaryParents,
  useGlossaryTermUsage,
  useGlossaryTermColumnUsage,
  useEntityDocuments,
  useDeleteGlossaryEntity,
  GLOSSARY_PAGE_LIMIT,
  type GlossaryNode,
  type GlossaryTerm,
  type TableSearchResult,
} from "@/api/portal/datahub";
import { KnowledgeBacklinks } from "@/components/knowledge/KnowledgeBacklinks";
import { ListSkeleton, Badge } from "./catalog/primitives";
import { shortUrn } from "./catalog/utils";
import {
  DeleteControl,
  EntityDescription,
  PageCapNotice,
  TableLink,
  type Usage,
} from "./catalog/governance";

// NO_CARRIERS is the one wording for "nothing uses this term", shared by the
// usage list and the delete confirmation so the two never disagree.
const NO_CARRIERS = "No table in this connection is annotated with this term.";

/**
 * GlossaryBreadcrumb shows where a glossary entity sits: the root, then each
 * ancestor node from the outermost in, then the entity itself. It reads the
 * parent chain rather than the path the user walked, so an entity opened from
 * anywhere shows the same place in the tree.
 *
 * A failed chain read says so instead of rendering a term as if it were at the
 * root, which is what an empty chain means.
 */
export function GlossaryBreadcrumb({
  conn,
  urn,
  self,
  onOpenNode,
  onOpenRoot,
}: {
  conn: string;
  urn: string;
  self: string;
  onOpenNode: (node: GlossaryNode) => void;
  onOpenRoot: () => void;
}) {
  const { data, isError } = useGlossaryParents(conn, urn);
  // The chain arrives direct-parent first; a breadcrumb reads outermost first.
  const ancestors = [...(data ?? [])].reverse();

  return (
    <nav aria-label="Glossary location" className="flex flex-wrap items-center gap-1 text-xs text-muted-foreground">
      <button onClick={onOpenRoot} className="inline-flex items-center gap-1 hover:text-foreground">
        <Library className="h-3.5 w-3.5" /> Glossary
      </button>
      {isError ? (
        <>
          <ChevronRight className="h-3 w-3" />
          <span className="italic">location unavailable</span>
        </>
      ) : (
        ancestors.map((n) => (
          <span key={n.urn} className="flex items-center gap-1">
            <ChevronRight className="h-3 w-3" />
            <button onClick={() => onOpenNode(n)} className="hover:text-foreground">
              {n.name || shortUrn(n.urn)}
            </button>
          </span>
        ))
      )}
      <ChevronRight className="h-3 w-3" />
      <span className="font-medium text-foreground">{self}</span>
    </nav>
  );
}

/**
 * GlossaryTermDetail is one term: what it means, where it sits, the notes
 * attached to it, and the tables it is applied to, plus the edits a steward can
 * make from here. The definition is edited through the shared entity-description
 * write — DataHub stores a term's text in the glossaryTermInfo aspect's
 * "definition" field, and the platform routes the write there by entity type.
 */
export function GlossaryTermDetail({
  conn,
  term,
  canEdit,
  canDelete,
  onBack,
  onOpenNode,
  onOpenRoot,
  onNavigate,
}: {
  conn: string;
  term: GlossaryTerm;
  canEdit: boolean;
  canDelete: boolean;
  onBack: () => void;
  onOpenNode: (node: GlossaryNode) => void;
  onOpenRoot: () => void;
  onNavigate?: (path: string) => void;
}) {
  const carriers = useGlossaryTermUsage(conn, term.urn);
  const tables = carriers.data ?? [];
  const usage: Usage = {
    loading: carriers.isLoading,
    failed: carriers.isError,
    count: tables.length,
  };

  return (
    <div className="space-y-4">
      <button
        onClick={onBack}
        className="inline-flex items-center gap-1.5 text-sm text-muted-foreground hover:text-foreground"
      >
        <ArrowLeft className="h-4 w-4" /> Back to the glossary
      </button>

      <GlossaryBreadcrumb
        conn={conn}
        urn={term.urn}
        self={term.name || shortUrn(term.urn)}
        onOpenNode={onOpenNode}
        onOpenRoot={onOpenRoot}
      />

      <div>
        <h2 className="flex items-center gap-2 text-lg font-semibold">
          <BookMarked className="h-4 w-4 text-muted-foreground" />
          {term.name || shortUrn(term.urn)}
        </h2>
        <p className="break-all text-xs text-muted-foreground">{term.urn}</p>
      </div>

      {canDelete && <TermDeleteControl conn={conn} term={term} usage={usage} onDeleted={onBack} />}

      <EntityDescription
        conn={conn}
        entity={term}
        canEdit={canEdit}
        label="Term definition"
        format="markdown"
      />

      <EntityDocuments conn={conn} urn={term.urn} />

      {/* The knowledge written about this term, from the reverse lookup over
          page references. It renders nothing when no accessible page cites it. */}
      <KnowledgeBacklinks urn={term.urn} onNavigate={onNavigate} />

      <TermUsage conn={conn} term={term} state={carriers} tables={tables} onNavigate={onNavigate} />
    </div>
  );
}

// EntityDocuments lists the context documents attached to a glossary entity —
// the long-form notes that do not fit a one-line definition. Documents are
// created and edited on the Context Docs tab, so this is a read: it says what is
// attached, and where to go to change it.
export function EntityDocuments({ conn, urn }: { conn: string; urn: string }) {
  const { data, isLoading, isError } = useEntityDocuments(conn, urn);
  const docs = data ?? [];

  return (
    <section className="space-y-2">
      <h3 className="text-sm font-medium">Context documents</h3>
      {isError ? (
        <p className="text-sm text-destructive">Failed to load the attached context documents.</p>
      ) : isLoading ? (
        <p className="text-xs text-muted-foreground">Loading…</p>
      ) : docs.length === 0 ? (
        <p className="rounded-md border border-dashed px-4 py-6 text-center text-sm text-muted-foreground">
          Nothing is attached. Attach a note on the Context Docs tab.
        </p>
      ) : (
        <ul className="space-y-2">
          {docs.map((d) => (
            <li key={d.urn} className="flex flex-col gap-0.5 rounded-lg border p-3">
              <span className="flex items-center gap-2 text-sm font-medium">
                <FileText className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
                {d.title}
                {d.sub_type && <Badge tone="primary">{d.sub_type}</Badge>}
              </span>
              {d.snippet && (
                <span className="line-clamp-2 text-xs text-muted-foreground">{d.snippet}</span>
              )}
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

// TermUsage lists the tables the term is applied to, marking the ones where the
// annotation is on a column rather than on the table.
//
// It takes two reads because DataHub has no table-level-only filter: its
// glossaryTerms field matches a table annotated on the table OR on one of its
// columns, and fieldGlossaryTerms matches only the column-level ones. Listing
// just the first would call a column annotation a table annotation.
function TermUsage({
  conn,
  term,
  state,
  tables,
  onNavigate,
}: {
  conn: string;
  term: GlossaryTerm;
  state: { isLoading: boolean; isError: boolean };
  tables: TableSearchResult[];
  onNavigate?: (path: string) => void;
}) {
  const columnCarriers = useGlossaryTermColumnUsage(conn, term.urn);
  // A failed or pending column read leaves the set empty, so a row is marked as
  // column-level only on evidence: an unmarked row means "not known to be", not
  // "known not to be".
  const onColumn = new Set((columnCarriers.data ?? []).map((t) => t.urn));

  return (
    <section className="space-y-2">
      <h3 className="text-sm font-medium">Tables annotated with this term</h3>
      {state.isError ? (
        <p className="text-sm text-destructive">Failed to load the tables using this term.</p>
      ) : state.isLoading ? (
        <ListSkeleton />
      ) : tables.length === 0 ? (
        <p className="rounded-md border border-dashed px-4 py-6 text-center text-sm text-muted-foreground">
          {NO_CARRIERS}
        </p>
      ) : (
        <>
          {/* The Tables tab searches name, description, and tag text, not
              glossary terms, so the honest overflow route is DataHub's own UI
              rather than a filter this portal does not offer. */}
          <PageCapNotice
            shown={tables.length}
            limit={GLOSSARY_PAGE_LIMIT}
            what="tables"
            hint="The rest are reachable in the DataHub UI."
          />
          <ul className="space-y-2">
            {tables.map((t) => (
              <li key={t.urn}>
                <TableLink
                  table={t}
                  onNavigate={onNavigate}
                  trailing={onColumn.has(t.urn) ? <Badge tone="primary">on a column</Badge> : undefined}
                />
              </li>
            ))}
          </ul>
        </>
      )}
    </section>
  );
}

// TermDeleteControl retires a term behind the shared confirmation, supplying the
// impact sentence specific to a term: how many tables in this connection are
// annotated with it.
function TermDeleteControl({
  conn,
  term,
  usage,
  onDeleted,
}: {
  conn: string;
  term: GlossaryTerm;
  usage: Usage;
  onDeleted: () => void;
}) {
  const del = useDeleteGlossaryEntity(conn);
  return (
    <DeleteControl
      label="Delete term"
      impact={<DeleteImpact usage={usage} />}
      mut={del}
      onConfirm={() => del.mutate(term.urn, { onSuccess: onDeleted })}
    />
  );
}

// DeleteImpact states what the delete will affect, in each state the usage read
// can be in. A failed read says so: reporting "nothing uses this term" from a
// read that never answered would understate the delete. The delete itself is one
// upstream mutation against the term entity and touches no table, which is why
// the annotations are described as left behind rather than as cleared.
function DeleteImpact({ usage }: { usage: Usage }) {
  if (usage.loading) return <>Checking what uses this term…</>;
  if (usage.failed) {
    return <>Could not check what uses this term, so the effect of deleting it is unknown.</>;
  }
  if (usage.count === 0) return <>{NO_CARRIERS}</>;
  // The usage read is one page, so a full page is a floor, not a count.
  const atCap = usage.count >= GLOSSARY_PAGE_LIMIT;
  const used =
    usage.count === 1
      ? "1 table in this connection is annotated with this term."
      : `${atCap ? "At least " : ""}${usage.count} tables in this connection are annotated with this term.`;
  return <>{used} Deleting removes the term definition from DataHub; it does not remove the annotation from those tables.</>;
}
