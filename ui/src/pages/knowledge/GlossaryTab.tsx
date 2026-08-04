import { useState } from "react";
import { ArrowLeft, BookMarked, Folder, Plus } from "lucide-react";
import {
  useGlossaryRoots,
  useGlossaryChildren,
  useCreateGlossaryTerm,
  useCreateGlossaryNode,
  useDeleteGlossaryEntity,
  type GlossaryChildren,
  type GlossaryNode,
  type GlossaryRoots,
  type GlossaryTerm,
} from "@/api/portal/datahub";
import { useConnectionWritable } from "@/components/knowledge/DataHubConnectionSelect";
import { useAuthStore } from "@/stores/auth";
import { ListSkeleton, MutationError } from "./catalog/primitives";
import { shortUrn } from "./catalog/utils";
import { DeleteControl, EntityDescription, VocabCard } from "./catalog/governance";
import { EntityDocuments, GlossaryBreadcrumb, GlossaryTermDetail } from "./GlossaryDetail";

/**
 * GlossaryTab is the Glossary tab of the Catalog section (#1158): the business
 * vocabulary itself — the terms this organization defines and the nodes that
 * organize them — rather than the terms carried by one table. It walks the
 * hierarchy one branch at a time, opens a term to show its definition, its place
 * in the tree, the context documents attached to it, and the tables annotated
 * with it, and — when the persona grants the matching datahub tool and the
 * connection is write-enabled — creates a term or a node, edits a definition,
 * and retires a term or an empty node. The API enforces the same gates. The
 * connection is chosen by CatalogSection, which remounts this on a change so an
 * open branch never outlives the connection it belongs to.
 */
export function GlossaryTab({
  conn,
  onNavigate,
}: {
  conn: string;
  onNavigate?: (path: string) => void;
}) {
  // node is the branch being browsed (null at the root); term is the open term.
  // A node is both a place in the tree and an entity with its own definition, so
  // browsing into one IS its detail view rather than a separate screen.
  const [node, setNode] = useState<GlossaryNode | null>(null);
  const [term, setTerm] = useState<GlossaryTerm | null>(null);
  const [creating, setCreating] = useState<"term" | "node" | null>(null);
  const writable = useConnectionWritable(conn);
  const tools = useAuthStore((s) => s.user?.tools);
  const isAdmin = useAuthStore((s) => s.isAdmin());
  const has = (t: string) => (tools?.includes(t) ?? false) || isAdmin;
  const canCreate = writable && has("datahub_create");
  const canEdit = writable && has("datahub_update");
  const canDelete = writable && has("datahub_delete");

  const openNode = (n: GlossaryNode | null) => {
    setTerm(null);
    setCreating(null);
    setNode(n);
  };

  if (term) {
    return (
      <GlossaryTermDetail
        key={term.urn}
        conn={conn}
        term={term}
        canEdit={canEdit}
        canDelete={canDelete}
        onBack={() => setTerm(null)}
        onOpenNode={openNode}
        onOpenRoot={() => openNode(null)}
        onNavigate={onNavigate}
      />
    );
  }

  if (creating) {
    return (
      <GlossaryEntityForm
        conn={conn}
        kind={creating}
        parent={node}
        onDone={() => setCreating(null)}
      />
    );
  }

  const create = canCreate ? <CreateBar parent={node} onCreate={setCreating} /> : null;

  return node ? (
    <NodeBrowser
      key={node.urn}
      conn={conn}
      node={node}
      canEdit={canEdit}
      canDelete={canDelete}
      create={create}
      onOpenNode={openNode}
      onOpenTerm={setTerm}
      onOpenRoot={() => openNode(null)}
    />
  ) : (
    <RootBrowser conn={conn} create={create} onOpenNode={openNode} onOpenTerm={setTerm} />
  );
}

// RootBrowser lists the top of the glossary: the nodes and terms with no parent.
function RootBrowser({
  conn,
  create,
  onOpenNode,
  onOpenTerm,
}: {
  conn: string;
  create: React.ReactNode;
  onOpenNode: (node: GlossaryNode) => void;
  onOpenTerm: (term: GlossaryTerm) => void;
}) {
  const { data, isLoading, isError } = useGlossaryRoots(conn);

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 className="text-lg font-semibold">Glossary</h2>
        {create}
      </div>
      <GlossaryBranch
        branch={rootBranch(data)}
        isLoading={isLoading}
        isError={isError}
        emptyMessage="This connection has no glossary yet."
        onOpenNode={onOpenNode}
        onOpenTerm={onOpenTerm}
      />
    </div>
  );
}

// NodeBrowser is one node: where it sits, what it means, what is inside it, and
// the edits a steward can make from here.
function NodeBrowser({
  conn,
  node,
  canEdit,
  canDelete,
  create,
  onOpenNode,
  onOpenTerm,
  onOpenRoot,
}: {
  conn: string;
  node: GlossaryNode;
  canEdit: boolean;
  canDelete: boolean;
  create: React.ReactNode;
  onOpenNode: (node: GlossaryNode | null) => void;
  onOpenTerm: (term: GlossaryTerm) => void;
  onOpenRoot: () => void;
}) {
  const { data, isLoading, isError } = useGlossaryChildren(conn, node.urn);

  return (
    <div className="space-y-4">
      <button
        onClick={onOpenRoot}
        className="inline-flex items-center gap-1.5 text-sm text-muted-foreground hover:text-foreground"
      >
        <ArrowLeft className="h-4 w-4" /> Back to the glossary
      </button>

      <GlossaryBreadcrumb
        conn={conn}
        urn={node.urn}
        self={node.name || shortUrn(node.urn)}
        onOpenNode={onOpenNode}
        onOpenRoot={onOpenRoot}
      />

      <div>
        <h2 className="flex items-center gap-2 text-lg font-semibold">
          <Folder className="h-4 w-4 text-muted-foreground" />
          {node.name || shortUrn(node.urn)}
        </h2>
        <p className="break-all text-xs text-muted-foreground">{node.urn}</p>
      </div>

      {canDelete && (
        <NodeDeleteControl
          conn={conn}
          node={node}
          contents={data}
          isError={isError}
          onDeleted={onOpenRoot}
        />
      )}

      <EntityDescription conn={conn} entity={node} canEdit={canEdit} label="Node definition" />

      <EntityDocuments conn={conn} urn={node.urn} />

      <div className="flex flex-wrap items-center justify-between gap-2">
        <h3 className="text-sm font-medium">In this node</h3>
        {create}
      </div>
      <GlossaryBranch
        branch={nodeBranch(data)}
        isLoading={isLoading}
        isError={isError}
        emptyMessage="This node is empty."
        onOpenNode={onOpenNode}
        onOpenTerm={onOpenTerm}
      />
    </div>
  );
}

// Branch is one level of the tree in the shape the renderer needs, which is not
// the shape either read returns: the roots read pages nodes and terms with a
// total each, a node's children come back as one mixed page with one total.
// rootBranch and nodeBranch reconcile them so the renderer has a single case.
interface Branch {
  nodes: GlossaryNode[];
  terms: GlossaryTerm[];
  total: number;
}

const EMPTY_BRANCH: Branch = { nodes: [], terms: [], total: 0 };

function rootBranch(data: GlossaryRoots | undefined): Branch {
  if (!data) return EMPTY_BRANCH;
  return { nodes: data.nodes, terms: data.terms, total: data.nodes_total + data.terms_total };
}

function nodeBranch(data: GlossaryChildren | undefined): Branch {
  if (!data) return EMPTY_BRANCH;
  return { nodes: data.nodes, terms: data.terms, total: data.total };
}

// GlossaryBranch renders one level of the tree: its nodes first, then its terms,
// so the structure reads before the vocabulary inside it.
function GlossaryBranch({
  branch,
  isLoading,
  isError,
  emptyMessage,
  onOpenNode,
  onOpenTerm,
}: {
  branch: Branch;
  isLoading: boolean;
  isError: boolean;
  emptyMessage: string;
  onOpenNode: (node: GlossaryNode) => void;
  onOpenTerm: (term: GlossaryTerm) => void;
}) {
  const { nodes, terms, total } = branch;
  const shown = nodes.length + terms.length;

  if (isError) {
    return (
      <p className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">
        Failed to load the glossary.
      </p>
    );
  }
  if (isLoading) return <ListSkeleton />;
  if (shown === 0) {
    return (
      <p className="rounded-md border border-dashed px-4 py-8 text-center text-sm text-muted-foreground">
        {emptyMessage}
      </p>
    );
  }

  return (
    <div className="space-y-2">
      {/* The total is the backend's, not this page's: a branch wider than one
          page says how much it is not showing rather than presenting the page
          as the whole branch. */}
      {shown < total && (
        <p className="rounded-md border border-dashed px-3 py-2 text-xs text-muted-foreground">
          Showing {shown} of {total}. The rest are reachable in the DataHub UI.
        </p>
      )}
      <ul className="grid gap-2 sm:grid-cols-2">
        {nodes.map((n) => (
          <li key={n.urn}>
            <VocabCard entry={n} icon={Folder} onOpen={() => onOpenNode(n)} />
          </li>
        ))}
        {terms.map((t) => (
          <li key={t.urn}>
            <VocabCard entry={t} icon={BookMarked} onOpen={() => onOpenTerm(t)} />
          </li>
        ))}
      </ul>
    </div>
  );
}

// CreateBar offers both creates, naming where the new entity will land so a
// steward is never guessing which branch they are adding to.
function CreateBar({
  parent,
  onCreate,
}: {
  parent: GlossaryNode | null;
  onCreate: (kind: "term" | "node") => void;
}) {
  const where = parent ? `in ${parent.name || shortUrn(parent.urn)}` : "at the root";
  return (
    <div className="flex items-center gap-2">
      <button
        onClick={() => onCreate("term")}
        aria-label={`New term ${where}`}
        className="inline-flex items-center gap-1.5 rounded-md bg-primary px-3 py-2 text-sm font-medium text-primary-foreground hover:bg-primary/90"
      >
        <Plus className="h-4 w-4" /> New term
      </button>
      <button
        onClick={() => onCreate("node")}
        aria-label={`New node ${where}`}
        className="inline-flex items-center gap-1.5 rounded-md border px-3 py-2 text-sm font-medium hover:bg-muted"
      >
        <Plus className="h-4 w-4" /> New node
      </button>
    </div>
  );
}

// NodeDeleteControl retires an empty node. A node with children gets the reason
// in place of the button rather than a delete at all: DataHub takes the node
// without taking what is inside it, so the honest options are "empty it first"
// or "leave it", and saying which beats a confirmation that cannot state its
// own outcome.
function NodeDeleteControl({
  conn,
  node,
  contents,
  isError,
  onDeleted,
}: {
  conn: string;
  node: GlossaryNode;
  contents: GlossaryChildren | undefined;
  isError: boolean;
  onDeleted: () => void;
}) {
  const del = useDeleteGlossaryEntity(conn);

  // The delete is offered only on a read that answered and answered empty. A
  // failed read says so rather than falling through to either "it is empty" or
  // "it holds entries", neither of which it established.
  if (isError) {
    return (
      <p className="rounded-md border border-dashed px-3 py-2 text-xs text-muted-foreground">
        Could not read what is in this node, so its delete is not offered.
      </p>
    );
  }
  if (!contents) {
    return (
      <p className="rounded-md border border-dashed px-3 py-2 text-xs text-muted-foreground">
        Checking what is in this node before offering to delete it.
      </p>
    );
  }
  if (contents.total > 0) {
    return (
      <p className="rounded-md border border-dashed px-3 py-2 text-xs text-muted-foreground">
        This node holds {contents.total} {contents.total === 1 ? "entry" : "entries"}. Move or
        delete them before deleting the node.
      </p>
    );
  }
  return (
    <DeleteControl
      label="Delete node"
      impact={<>This node is empty. Deleting removes it from DataHub.</>}
      mut={del}
      onConfirm={() => del.mutate(node.urn, { onSuccess: onDeleted })}
    />
  );
}

// GlossaryEntityForm creates a term or a node under the branch being browsed.
// Both kinds take the same three fields, so they share the form and differ only
// in wording and in which write runs.
function GlossaryEntityForm({
  conn,
  kind,
  parent,
  onDone,
}: {
  conn: string;
  kind: "term" | "node";
  parent: GlossaryNode | null;
  onDone: () => void;
}) {
  const createTerm = useCreateGlossaryTerm(conn);
  const createNode = useCreateGlossaryNode(conn);
  const create = kind === "term" ? createTerm : createNode;
  const [name, setName] = useState("");
  const [definition, setDefinition] = useState("");

  const copy =
    kind === "term"
      ? {
          heading: "New term",
          namePlaceholder: "e.g. Net Revenue",
          definitionPlaceholder: "What this term means, and how it is calculated.",
          submit: "Create term",
        }
      : {
          heading: "New node",
          namePlaceholder: "e.g. Finance",
          definitionPlaceholder: "What this part of the glossary covers.",
          submit: "Create node",
        };

  return (
    <div className="space-y-4">
      <button
        onClick={onDone}
        className="inline-flex items-center gap-1.5 text-sm text-muted-foreground hover:text-foreground"
      >
        <ArrowLeft className="h-4 w-4" /> Cancel
      </button>
      <h2 className="text-lg font-semibold">{copy.heading}</h2>
      <p className="text-sm text-muted-foreground">
        {parent
          ? `Created in ${parent.name || shortUrn(parent.urn)}.`
          : "Created at the root of the glossary."}
      </p>

      <label className="block space-y-1">
        <span className="text-sm font-medium">Name</span>
        <input
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder={copy.namePlaceholder}
          className="w-full rounded-md border bg-background px-2 py-1.5 text-sm outline-none ring-ring focus:ring-2"
        />
      </label>

      <label className="block space-y-1">
        <span className="text-sm font-medium">Definition</span>
        <textarea
          value={definition}
          onChange={(e) => setDefinition(e.target.value)}
          rows={3}
          placeholder={copy.definitionPlaceholder}
          className="w-full rounded-md border bg-background px-2 py-1.5 text-sm outline-none ring-ring focus:ring-2"
        />
      </label>

      <p className="text-xs text-muted-foreground">
        DataHub indexes the glossary asynchronously, so what you create may take a moment to appear
        in the branch.
      </p>

      <MutationError mut={create} />

      <div className="flex gap-2">
        <button
          onClick={() =>
            create.mutate(
              {
                name: name.trim(),
                definition: definition.trim() || undefined,
                parent_node: parent?.urn,
              },
              { onSuccess: onDone },
            )
          }
          disabled={name.trim() === "" || create.isPending}
          className="rounded-md bg-primary px-4 py-1.5 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
        >
          {copy.submit}
        </button>
        <button onClick={onDone} className="rounded-md border px-4 py-1.5 text-sm hover:bg-muted">
          Cancel
        </button>
      </div>
    </div>
  );
}
