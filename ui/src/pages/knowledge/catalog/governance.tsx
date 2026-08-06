import { useState } from "react";
import { ArrowLeft, Trash2, type LucideIcon } from "lucide-react";
import { useUpdateDescription, type EntityRef, type TableSearchResult } from "@/api/portal/datahub";
import { MarkdownEditor } from "@/components/MarkdownEditor";
import { MarkdownRenderer } from "@/components/renderers/MarkdownRenderer";
import { catalogHref } from "@/lib/entityRefs";
import { ListSkeleton, MutationError } from "./primitives";
import { shortUrn } from "./utils";

// Shared surfaces for the DataHub governance vocabularies under Catalog: Tags
// (#1156) and Domains (#1157). Both list a vocabulary, open one entry to see
// what it means and which tables it covers, and edit that entry through the same
// three writes. What differs between them is the wording and the write routes,
// not these mechanics, so they live here once rather than per kind.

// PageCapNotice states that a read came back full, so a capped list is never
// presented as the whole set. limit is what the read can actually return, which
// is not always what the surface asked for: the domain list is capped upstream.
export function PageCapNotice({
  shown,
  limit,
  what,
  hint,
}: {
  shown: number;
  limit: number;
  what: string;
  hint: string;
}) {
  if (shown < limit) return null;
  return (
    <p className="rounded-md border border-dashed px-3 py-2 text-xs text-muted-foreground">
      Showing the first {limit} {what}; there may be more. {hint}
    </p>
  );
}

// VocabCard is one entry in a vocabulary list: its name, its description, and
// the click that opens it. An entry with no description says so rather than
// rendering an empty line, so an undocumented tag or domain is visible as a gap
// to fill rather than as whitespace.
export function VocabCard({
  entry,
  icon: Icon,
  onOpen,
}: {
  entry: EntityRef;
  icon: LucideIcon;
  onOpen: () => void;
}) {
  return (
    <button
      onClick={onOpen}
      className="flex h-full w-full flex-col gap-1 rounded-lg border p-3 text-left transition-colors hover:border-primary/50 hover:bg-muted/50"
    >
      <span className="flex items-center gap-2 text-sm font-medium">
        <Icon className="h-4 w-4 shrink-0 text-muted-foreground" />
        {entry.name || shortUrn(entry.urn)}
      </span>
      {entry.description ? (
        <span className="line-clamp-2 text-xs text-muted-foreground">{entry.description}</span>
      ) : (
        <span className="text-xs italic text-muted-foreground">No description</span>
      )}
    </button>
  );
}

// TableLink renders one table a vocabulary entry covers. It deep-links into the
// Tables tab's entity editor through the shared catalogHref, and stays a plain
// row when there is no navigator or the URN is not a catalog reference, so it is
// never styled as a link it cannot follow. trailing renders alongside the row
// (a membership remove control, for the Domains tab).
export function TableLink({
  table,
  onNavigate,
  trailing,
}: {
  table: TableSearchResult;
  onNavigate?: (path: string) => void;
  trailing?: React.ReactNode;
}) {
  const href = catalogHref(table.urn);
  const body = (
    <>
      <span className="text-sm font-medium">{table.name || shortUrn(table.urn)}</span>
      {table.description && (
        <span className="line-clamp-2 text-xs text-muted-foreground">{table.description}</span>
      )}
    </>
  );
  const shell = "flex flex-col gap-0.5 rounded-lg border p-3";

  const row =
    !href || !onNavigate ? (
      <div className={`${shell} ${trailing ? "flex-1" : ""}`}>{body}</div>
    ) : (
      <a
        href={href}
        onClick={(e) => {
          e.preventDefault();
          onNavigate(href);
        }}
        className={`${shell} transition-colors hover:border-primary/50 hover:bg-muted/50 ${trailing ? "flex-1" : ""}`}
      >
        {body}
      </a>
    );

  if (!trailing) return row;
  return (
    <div className="flex items-center gap-2">
      {row}
      {trailing}
    </div>
  );
}

// EntityDescription renders a vocabulary entry's description and, for an editor,
// the edit form. The save is the shared entity-description write with the
// entry's own URN: the platform's UpdateDescription routes by entity type, so a
// tag, a domain, and a table are all edited through the one route. label names
// the editing surface for assistive technology and for the tests that drive it.
//
// format is the caller's, not something read off the URN, because what decides
// it is what DataHub renders for that kind: it renders a domain, a glossary
// term, and a glossary node as markdown, and a tag as plain text (#1200).
// Inferring markdown from a URN prefix would make that a guess rather than a
// statement, and would silently give tags an editor whose output renders as raw
// source everywhere else in the catalog.
export function EntityDescription({
  conn,
  entity,
  canEdit,
  label,
  format,
}: {
  conn: string;
  entity: EntityRef;
  canEdit: boolean;
  label: string;
  format: "markdown" | "plain";
}) {
  const update = useUpdateDescription(conn);
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(entity.description ?? "");
  // The mutation's own result is the freshest description on this screen: the
  // entry came from a list read that a save does not refetch into this component.
  const current = update.isSuccess ? draft : (entity.description ?? "");

  if (editing) {
    return (
      <section className="space-y-2">
        <h3 className="text-sm font-medium">Description</h3>
        {format === "markdown" ? (
          <MarkdownEditor
            value={draft}
            onChange={setDraft}
            label={label}
            minHeight="240px"
            placeholder="Describe this in markdown…"
          />
        ) : (
          <textarea
            aria-label={label}
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            rows={3}
            className="w-full rounded-md border bg-background px-2 py-1.5 text-sm outline-none ring-ring focus:ring-2"
          />
        )}
        <div className="flex gap-2">
          <button
            onClick={() =>
              update.mutate(
                { urn: entity.urn, description: draft.trim() },
                { onSuccess: () => setEditing(false) },
              )
            }
            disabled={update.isPending}
            className="rounded-md bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
          >
            Save
          </button>
          <button
            onClick={() => {
              setDraft(current);
              setEditing(false);
            }}
            className="rounded-md border px-3 py-1.5 text-sm hover:bg-muted"
          >
            Cancel
          </button>
        </div>
        <MutationError mut={update} />
      </section>
    );
  }

  return (
    <section className="space-y-2">
      <div className="flex items-center justify-between gap-2">
        <h3 className="text-sm font-medium">Description</h3>
        {canEdit && (
          <button
            onClick={() => {
              setDraft(current);
              setEditing(true);
            }}
            className="text-xs text-muted-foreground hover:text-foreground"
          >
            Edit description
          </button>
        )}
      </div>
      {!current ? (
        <p className="text-sm italic text-muted-foreground">No description</p>
      ) : format === "markdown" ? (
        <MarkdownRenderer content={current} bare />
      ) : (
        <p className="text-sm">{current}</p>
      )}
    </section>
  );
}

// DeleteControl retires a vocabulary entry behind a confirmation that states the
// blast radius first. impact is the caller's: deleting a tag and deleting a
// domain affect the tables they cover differently, so each kind says what its
// own delete does rather than sharing one sentence.
export function DeleteControl({
  label,
  impact,
  mut,
  onConfirm,
}: {
  label: string;
  impact: React.ReactNode;
  mut: { isPending: boolean; isError: boolean; error: unknown };
  onConfirm: () => void;
}) {
  const [confirming, setConfirming] = useState(false);

  return (
    <div className="space-y-2">
      <div className="flex justify-end gap-2">
        {confirming ? (
          <>
            <button
              onClick={onConfirm}
              disabled={mut.isPending}
              className="rounded-md bg-destructive px-3 py-1.5 text-sm font-medium text-destructive-foreground hover:bg-destructive/90 disabled:opacity-50"
            >
              Confirm delete
            </button>
            <button
              onClick={() => setConfirming(false)}
              className="rounded-md border px-3 py-1.5 text-sm hover:bg-muted"
            >
              Cancel
            </button>
          </>
        ) : (
          <button
            onClick={() => setConfirming(true)}
            className="inline-flex items-center gap-1.5 rounded-md border px-3 py-1.5 text-sm text-destructive hover:bg-destructive/10"
          >
            <Trash2 className="h-3.5 w-3.5" /> {label}
          </button>
        )}
      </div>
      {confirming && (
        <p className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm">
          {impact}
        </p>
      )}
      <MutationError mut={mut} />
    </div>
  );
}

// Usage is the state a "what does this entry cover" read can be in. A failed
// read is distinct from an empty one: reporting "nothing" from a read that never
// answered would understate a delete.
export interface Usage {
  loading: boolean;
  failed: boolean;
  count: number;
}

// BackToList is the return affordance a deep-linked view needs: the reader
// arrived from a knowledge page rather than from the list, so there is no
// browsing history within the tab to go back through.
export function BackToList({ label, onBack }: { label: string; onBack: () => void }) {
  return (
    <button
      onClick={onBack}
      className="inline-flex items-center gap-1.5 text-sm text-muted-foreground hover:text-foreground"
    >
      <ArrowLeft className="h-4 w-4" /> {label}
    </button>
  );
}

/**
 * DeepLinkedEntry opens the vocabulary entry a `?urn=` deep link addresses
 * (#1159), so a knowledge page's citation of a tag or a domain lands on that
 * entry rather than on the list.
 *
 * The entry comes from the list the tab already loads because neither
 * vocabulary has a by-URN read upstream: DataHub can list tags and domains, but
 * cannot fetch one by URN. So a URN the list does not hold — one from another
 * connection, one retired since the page cited it, or one past the cap the list
 * read returns — says exactly that instead of opening a detail view assembled
 * from the URN alone, which would show an empty description as if the entry had
 * none.
 */
export function DeepLinkedEntry({
  urn,
  entries,
  isLoading,
  isError,
  what,
  backLabel,
  onBack,
  children,
}: {
  urn: string;
  entries: EntityRef[] | undefined;
  isLoading: boolean;
  isError: boolean;
  // what names the kind in the miss and failure messages ("tag", "domain").
  what: string;
  backLabel: string;
  onBack: () => void;
  children: (entry: EntityRef) => React.ReactNode;
}) {
  if (isError || isLoading || !entries) {
    return (
      <div className="space-y-4">
        <BackToList label={backLabel} onBack={onBack} />
        {isError ? (
          <p className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">
            Failed to load {what}s, so the linked {what} could not be opened.
          </p>
        ) : (
          <ListSkeleton />
        )}
      </div>
    );
  }

  const entry = entries.find((e) => e.urn === urn);
  if (!entry) {
    return (
      <div className="space-y-4">
        <BackToList label={backLabel} onBack={onBack} />
        <p className="rounded-md border border-dashed px-4 py-6 text-sm text-muted-foreground">
          This connection lists no {what} with the URN{" "}
          <span className="break-all font-mono text-xs">{urn}</span>. It may belong to another
          connection, or have been retired since it was linked.
        </p>
      </div>
    );
  }
  return <>{children(entry)}</>;
}
