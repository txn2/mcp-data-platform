import { useState } from "react";
import { ArrowLeft, Search, Plus, Tag as TagIcon } from "lucide-react";
import {
  useTagList,
  useTagUsage,
  useCreateTag,
  useDeleteTag,
  TAG_LIST_LIMIT,
  type EntityRef,
} from "@/api/portal/datahub";
import { useConnectionWritable } from "@/components/knowledge/DataHubConnectionSelect";
import { KnowledgeBacklinks } from "@/components/knowledge/KnowledgeBacklinks";
import { useAuthStore } from "@/stores/auth";
import { useDebounced } from "@/lib/useDebounced";
import { ListSkeleton, MutationError } from "./catalog/primitives";
import { clearURNFromLocation, deepLinkedURN, shortUrn } from "./catalog/utils";
import {
  DeepLinkedEntry,
  DeleteControl,
  EntityDescription,
  PageCapNotice,
  TableLink,
  VocabCard,
  type Usage,
} from "./catalog/governance";

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
  // linked is the tag a `?urn=` deep link addresses (#1159): a knowledge page
  // citing a tag opens it here. It is a URN rather than an entry because the
  // link carries no name, and it is cleared from the URL on the way back so a
  // refresh does not reopen what the reader just left.
  const [linked, setLinked] = useState<string | null>(() => deepLinkedURN("tags"));
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

  const detail = (tag: EntityRef) => (
    <TagDetail
      key={tag.urn}
      conn={conn}
      tag={tag}
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
        <LinkedTag conn={conn} urn={linked} onBack={back}>
          {detail}
        </LinkedTag>
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

// LinkedTag resolves a deep-linked tag URN against this connection's tag list,
// which is the only read DataHub offers for a tag: there is no fetch-by-URN.
function LinkedTag({
  conn,
  urn,
  onBack,
  children,
}: {
  conn: string;
  urn: string;
  onBack: () => void;
  children: (tag: EntityRef) => React.ReactNode;
}) {
  const { data, isLoading, isError } = useTagList(conn, "");
  return (
    <DeepLinkedEntry
      urn={urn}
      entries={data}
      isLoading={isLoading}
      isError={isError}
      what="tag"
      backLabel="Back to tags"
      onBack={onBack}
    >
      {children}
    </DeepLinkedEntry>
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
            limit={TAG_LIST_LIMIT}
            what="tags"
            hint="Filter by name to reach the rest."
          />
          <ul className="grid gap-2 sm:grid-cols-2">
            {tags.map((t) => (
              <li key={t.urn}>
                <VocabCard entry={t} icon={TagIcon} onOpen={() => onOpen(t)} />
              </li>
            ))}
          </ul>
        </>
      )}
    </div>
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

      {/* Plain, not markdown: DataHub's own tag page renders this field as
          plain text, so a markdown editor here would invite formatting that
          shows as raw source everywhere else in the catalog (#1200). */}
      <EntityDescription
        conn={conn}
        entity={tag}
        canEdit={canEdit}
        label="Tag description"
        format="plain"
      />

      {/* The knowledge written about this tag, from the reverse lookup over
          page references. It renders nothing when no accessible page cites it. */}
      <KnowledgeBacklinks urn={tag.urn} onNavigate={onNavigate} />

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
              limit={TAG_LIST_LIMIT}
              what="tables"
              hint="Search the Tables tab by tag to see the rest."
            />
            <ul className="space-y-2">
              {carriers.map((d) => (
                <li key={d.urn}>
                  <TableLink table={d} onNavigate={onNavigate} />
                </li>
              ))}
            </ul>
          </>
        )}
      </section>
    </div>
  );
}

// TagDeleteControl retires a tag definition behind the shared confirmation,
// supplying the impact sentence that is specific to a tag: how many tables in
// this connection carry it. Deleting a tag nothing carries and deleting one the
// warehouse depends on look identical without it.
function TagDeleteControl({
  conn,
  tag,
  usage,
  onDeleted,
}: {
  conn: string;
  tag: EntityRef;
  usage: Usage;
  onDeleted: () => void;
}) {
  const del = useDeleteTag(conn);
  return (
    <DeleteControl
      label="Delete tag"
      impact={<DeleteImpact usage={usage} />}
      mut={del}
      onConfirm={() => del.mutate(tag.urn, { onSuccess: onDeleted })}
    />
  );
}

// DeleteImpact states what the delete will affect, in each state the usage read
// can be in. A failed read says so: reporting "nothing carries this tag" from a
// read that never answered would understate the delete.
function DeleteImpact({ usage }: { usage: Usage }) {
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
