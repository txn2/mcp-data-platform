import { useState } from "react";
import { ArrowLeft, Search, Plus, Tag as TagIcon, Trash2 } from "lucide-react";
import {
  useTagList,
  useTagUsage,
  useCreateTag,
  useDeleteTag,
  useUpdateDescription,
  TAG_LIST_LIMIT,
  type EntityRef,
  type TableSearchResult,
} from "@/api/portal/datahub";
import { useConnectionWritable } from "@/components/knowledge/DataHubConnectionSelect";
import { useAuthStore } from "@/stores/auth";
import { useDebounced } from "@/lib/useDebounced";
import { catalogHref } from "@/lib/entityRefs";
import { ListSkeleton, MutationError } from "./catalog/primitives";
import { shortUrn } from "./catalog/utils";

// NO_CARRIERS is the one wording for "nothing carries this tag", shared by the
// usage list and the delete confirmation so the two never disagree.
const NO_CARRIERS = "No table in this connection carries this tag.";

/**
 * TagsTab is the Tags tab of the Catalog section (#1156, #1194): the tag
 * vocabulary itself, rather than the tags carried by one table. It lists and
 * name-filters the tags on a DataHub connection, opens one to show its
 * description and the tables carrying it, and — when the persona grants the
 * matching datahub tool and the connection is write-enabled — creates a tag,
 * edits its description, and retires it. The API enforces the same gates. The
 * connection is chosen by CatalogSection, which remounts this on a change so an
 * open tag never outlives the connection it belongs to.
 */
export function TagsTab({
  conn,
  onNavigate,
}: {
  conn: string;
  onNavigate?: (path: string) => void;
}) {
  const [mode, setMode] = useState<"list" | "create">("list");
  const [selected, setSelected] = useState<EntityRef | null>(null);
  const writable = useConnectionWritable(conn);
  const tools = useAuthStore((s) => s.user?.tools);
  const isAdmin = useAuthStore((s) => s.isAdmin());
  const has = (t: string) => (tools?.includes(t) ?? false) || isAdmin;
  const canCreate = writable && has("datahub_create");
  const canEdit = writable && has("datahub_update");
  const canDelete = writable && has("datahub_delete");

  const back = () => {
    setSelected(null);
    setMode("list");
  };

  return (
    <div className="space-y-4">
      {selected ? (
        <TagDetail
          key={selected.urn}
          conn={conn}
          tag={selected}
          canEdit={canEdit}
          canDelete={canDelete}
          onBack={back}
          onNavigate={onNavigate}
        />
      ) : mode === "create" ? (
        <TagForm conn={conn} onDone={back} />
      ) : (
        <TagList
          conn={conn}
          canCreate={canCreate}
          onOpen={setSelected}
          onCreate={() => setMode("create")}
        />
      )}
    </div>
  );
}

function TagList({
  conn,
  canCreate,
  onOpen,
  onCreate,
}: {
  conn: string;
  canCreate: boolean;
  onOpen: (tag: EntityRef) => void;
  onCreate: () => void;
}) {
  const [query, setQuery] = useState("");
  const debounced = useDebounced(query, 250);
  const { data: tags, isLoading, isError } = useTagList(conn, debounced);

  return (
    <div className="space-y-4">
      <div className="flex items-center gap-2">
        <div className="relative flex-1">
          <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
          <input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Filter tags by name…"
            className="w-full rounded-md border bg-background py-2 pl-9 pr-3 text-sm outline-none ring-ring focus:ring-2"
          />
        </div>
        {canCreate && (
          <button
            onClick={onCreate}
            className="inline-flex items-center gap-1.5 rounded-md bg-primary px-3 py-2 text-sm font-medium text-primary-foreground hover:bg-primary/90"
          >
            <Plus className="h-4 w-4" /> New tag
          </button>
        )}
      </div>

      {isError ? (
        <p className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">
          Failed to load tags.
        </p>
      ) : isLoading ? (
        <ListSkeleton />
      ) : !tags || tags.length === 0 ? (
        <p className="rounded-md border border-dashed px-4 py-8 text-center text-sm text-muted-foreground">
          {debounced.trim()
            ? "No tags match that name."
            : "This connection has no tags yet."}
        </p>
      ) : (
        <>
          <PageCapNotice
            shown={tags.length}
            what="tags"
            hint="Filter by name to reach the rest."
          />
          <ul className="grid gap-2 sm:grid-cols-2">
            {tags.map((t) => (
              <li key={t.urn}>
                <button
                  onClick={() => onOpen(t)}
                  className="flex h-full w-full flex-col gap-1 rounded-lg border p-3 text-left transition-colors hover:border-primary/50 hover:bg-muted/50"
                >
                  <span className="flex items-center gap-2 text-sm font-medium">
                    <TagIcon className="h-4 w-4 shrink-0 text-muted-foreground" />
                    {t.name || shortUrn(t.urn)}
                  </span>
                  {t.description ? (
                    <span className="line-clamp-2 text-xs text-muted-foreground">
                      {t.description}
                    </span>
                  ) : (
                    <span className="text-xs italic text-muted-foreground">No description</span>
                  )}
                </button>
              </li>
            ))}
          </ul>
        </>
      )}
    </div>
  );
}

// PageCapNotice states that a read came back full, so a capped list is never
// presented as the whole set. Both tag reads page at TAG_LIST_LIMIT, which is
// what the server will actually return.
function PageCapNotice({
  shown,
  what,
  hint,
}: {
  shown: number;
  what: string;
  hint: string;
}) {
  if (shown < TAG_LIST_LIMIT) return null;
  return (
    <p className="rounded-md border border-dashed px-3 py-2 text-xs text-muted-foreground">
      Showing the first {TAG_LIST_LIMIT} {what}; there may be more. {hint}
    </p>
  );
}

function TagDetail({
  conn,
  tag,
  canEdit,
  canDelete,
  onBack,
  onNavigate,
}: {
  conn: string;
  tag: EntityRef;
  canEdit: boolean;
  canDelete: boolean;
  onBack: () => void;
  onNavigate?: (path: string) => void;
}) {
  const usage = useTagUsage(conn, tag.urn);
  const carriers = usage.data ?? [];

  return (
    <div className="space-y-4">
      <button
        onClick={onBack}
        className="inline-flex items-center gap-1.5 text-sm text-muted-foreground hover:text-foreground"
      >
        <ArrowLeft className="h-4 w-4" /> Back to tags
      </button>

      <div>
        <h2 className="flex items-center gap-2 text-lg font-semibold">
          <TagIcon className="h-4 w-4 text-muted-foreground" />
          {tag.name || shortUrn(tag.urn)}
        </h2>
        <p className="break-all text-xs text-muted-foreground">{tag.urn}</p>
      </div>

      {canDelete && (
        <TagDeleteControl
          conn={conn}
          tag={tag}
          usage={{ loading: usage.isLoading, failed: usage.isError, count: carriers.length }}
          onDeleted={onBack}
        />
      )}

      <TagDescription conn={conn} tag={tag} canEdit={canEdit} />

      <section className="space-y-2">
        <h3 className="text-sm font-medium">Tables carrying this tag</h3>
        {usage.isError ? (
          <p className="text-sm text-destructive">Failed to load the tables carrying this tag.</p>
        ) : usage.isLoading ? (
          <ListSkeleton />
        ) : carriers.length === 0 ? (
          <p className="rounded-md border border-dashed px-4 py-6 text-center text-sm text-muted-foreground">
            {NO_CARRIERS}
          </p>
        ) : (
          <>
            <PageCapNotice
              shown={carriers.length}
              what="tables"
              hint="Search the Tables tab by tag to see the rest."
            />
            <ul className="space-y-2">
              {carriers.map((d) => (
                <li key={d.urn}>
                  <CarrierLink table={d} onNavigate={onNavigate} />
                </li>
              ))}
            </ul>
          </>
        )}
      </section>
    </div>
  );
}

// TagDeleteControl retires a tag definition behind a confirmation that states
// the blast radius first: how many tables in this connection carry the tag.
// Deleting a tag nothing carries and deleting one the warehouse depends on look
// identical without it.
function TagDeleteControl({
  conn,
  tag,
  usage,
  onDeleted,
}: {
  conn: string;
  tag: EntityRef;
  usage: { loading: boolean; failed: boolean; count: number };
  onDeleted: () => void;
}) {
  const del = useDeleteTag(conn);
  const [confirming, setConfirming] = useState(false);

  return (
    <div className="space-y-2">
      <div className="flex justify-end gap-2">
        {confirming ? (
          <>
            <button
              onClick={() => del.mutate(tag.urn, { onSuccess: onDeleted })}
              disabled={del.isPending}
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
            <Trash2 className="h-3.5 w-3.5" /> Delete tag
          </button>
        )}
      </div>
      {confirming && (
        <p className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm">
          <DeleteImpact usage={usage} />
        </p>
      )}
      <MutationError mut={del} />
    </div>
  );
}

// DeleteImpact states what the delete will affect, in each state the usage read
// can be in. A failed read says so: reporting "nothing carries this tag" from a
// read that never answered would understate the delete.
function DeleteImpact({ usage }: { usage: { loading: boolean; failed: boolean; count: number } }) {
  if (usage.loading) return <>Checking what carries this tag…</>;
  if (usage.failed) {
    return <>Could not check what carries this tag, so the effect of deleting it is unknown.</>;
  }
  if (usage.count === 0) return <>{NO_CARRIERS}</>;
  // The usage read is one page, so a full page is a floor, not a count.
  const atCap = usage.count >= TAG_LIST_LIMIT;
  const carried =
    usage.count === 1
      ? "1 table in this connection carries this tag."
      : `${atCap ? "At least " : ""}${usage.count} tables in this connection carry this tag.`;
  return <>{carried} Deleting removes the tag definition from DataHub.</>;
}

// CarrierLink renders one table carrying the tag. It deep-links into the
// Tables tab's entity editor through the shared catalogHref, and stays a plain
// row when there is no navigator or the URN is not a catalog reference, so it is
// never styled as a link it cannot follow.
function CarrierLink({
  table,
  onNavigate,
}: {
  table: TableSearchResult;
  onNavigate?: (path: string) => void;
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

  if (!href || !onNavigate) {
    return <div className={shell}>{body}</div>;
  }
  return (
    <a
      href={href}
      onClick={(e) => {
        e.preventDefault();
        onNavigate(href);
      }}
      className={`${shell} transition-colors hover:border-primary/50 hover:bg-muted/50`}
    >
      {body}
    </a>
  );
}

// TagDescription renders a tag's description and, for an editor, the edit form.
// The save is the shared entity-description write with the tag's URN: DataHub
// stores a tag's text in the tagProperties aspect, and the platform's
// UpdateDescription already routes by entity type.
function TagDescription({
  conn,
  tag,
  canEdit,
}: {
  conn: string;
  tag: EntityRef;
  canEdit: boolean;
}) {
  const update = useUpdateDescription(conn);
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(tag.description ?? "");
  // The mutation's own result is the freshest description on this screen: the
  // tag came from a list read that a save does not refetch into this component.
  const current = update.isSuccess ? draft : (tag.description ?? "");

  if (editing) {
    return (
      <section className="space-y-2">
        <h3 className="text-sm font-medium">Description</h3>
        <textarea
          aria-label="Tag description"
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          rows={3}
          className="w-full rounded-md border bg-background px-2 py-1.5 text-sm outline-none ring-ring focus:ring-2"
        />
        <div className="flex gap-2">
          <button
            onClick={() =>
              update.mutate(
                { urn: tag.urn, description: draft.trim() },
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
      {current ? (
        <p className="text-sm">{current}</p>
      ) : (
        <p className="text-sm italic text-muted-foreground">No description</p>
      )}
    </section>
  );
}

function TagForm({ conn, onDone }: { conn: string; onDone: () => void }) {
  const create = useCreateTag(conn);
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
      <h2 className="text-lg font-semibold">New tag</h2>

      <label className="block space-y-1">
        <span className="text-sm font-medium">Name</span>
        <input
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="e.g. certified"
          className="w-full rounded-md border bg-background px-2 py-1.5 text-sm outline-none ring-ring focus:ring-2"
        />
      </label>

      <label className="block space-y-1">
        <span className="text-sm font-medium">Description</span>
        <textarea
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          rows={3}
          placeholder="What this tag means, and when to apply it."
          className="w-full rounded-md border bg-background px-2 py-1.5 text-sm outline-none ring-ring focus:ring-2"
        />
      </label>

      <p className="text-xs text-muted-foreground">
        DataHub indexes new tags asynchronously, so a tag you create may take a moment to appear in
        the list.
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
          Create tag
        </button>
        <button onClick={onDone} className="rounded-md border px-4 py-1.5 text-sm hover:bg-muted">
          Cancel
        </button>
      </div>
    </div>
  );
}
