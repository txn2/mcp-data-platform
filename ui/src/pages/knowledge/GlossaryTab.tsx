import { useState } from "react";
import { Plus } from "lucide-react";
import {
  useGlossaryRoots,
  useGlossaryTerm,
  type GlossaryNode,
  type GlossaryTerm,
} from "@/api/portal/datahub";
import { ApiError } from "@/api/portal/client";
import { EmptyState } from "@/components/patterns/EmptyState";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { useConnectionWritable } from "@/components/knowledge/DataHubConnectionSelect";
import { useAuthStore } from "@/stores/auth";
import { ListSkeleton } from "./catalog/primitives";
import { clearURNFromLocation, deepLinkedURN, shortUrn } from "./catalog/utils";
import { BackToList } from "./catalog/governance";
import { GlossaryTermDetail } from "./GlossaryDetail";
import { GlossaryBranch, rootBranch } from "./glossary/GlossaryBranch";
import { GlossaryEntityForm } from "./glossary/GlossaryEntityForm";
import { NodeBrowser } from "./glossary/NodeBrowser";

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
  // linked is the term a `?urn=` deep link addresses (#1159): a knowledge page
  // citing a term opens it here, read by URN rather than walked to through the
  // tree. Cleared from the URL on the way back so a refresh does not reopen it.
  const [linked, setLinked] = useState<string | null>(() => deepLinkedURN("glossary"));
  const writable = useConnectionWritable(conn);
  const tools = useAuthStore((s) => s.user?.tools);
  const isAdmin = useAuthStore((s) => s.isAdmin());
  const has = (t: string) => (tools?.includes(t) ?? false) || isAdmin;
  const canCreate = writable && has("datahub_create");
  const canEdit = writable && has("datahub_update");
  const canDelete = writable && has("datahub_delete");

  // Leaving a term drops the deep link with it, so the back button on a linked
  // term returns to the glossary rather than reopening the term on refresh.
  const closeTerm = () => {
    setTerm(null);
    setLinked(null);
    clearURNFromLocation();
  };

  const openNode = (n: GlossaryNode | null) => {
    closeTerm();
    setCreating(null);
    setNode(n);
  };

  const termDetail = (t: GlossaryTerm) => (
    <GlossaryTermDetail
      key={t.urn}
      conn={conn}
      term={t}
      canEdit={canEdit}
      canDelete={canDelete}
      onBack={closeTerm}
      onOpenNode={openNode}
      onOpenRoot={() => openNode(null)}
      onNavigate={onNavigate}
    />
  );

  if (term) {
    return termDetail(term);
  }

  if (linked) {
    return (
      <LinkedTerm conn={conn} urn={linked} onBack={closeTerm}>
        {termDetail}
      </LinkedTerm>
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

// LinkedTerm opens a deep-linked term by URN. Unlike a tag or a domain, a term
// has a by-URN read upstream, so a term the connection holds always opens with
// its real definition however deep in the tree it sits; a URN this connection
// does not know answers 404, which is reported as the miss it is.
function LinkedTerm({
  conn,
  urn,
  onBack,
  children,
}: {
  conn: string;
  urn: string;
  onBack: () => void;
  children: (term: GlossaryTerm) => React.ReactNode;
}) {
  const { data, isLoading, isError, error } = useGlossaryTerm(conn, urn);

  if (data) return <>{children(data)}</>;
  // A term this connection does not hold and a read that failed are different
  // answers: only the 404 establishes that the term is not here, so a backend
  // failure says so rather than reporting the term as gone.
  const missing = error instanceof ApiError && error.status === 404;
  return (
    <div className="space-y-4">
      <BackToList label="Back to the glossary" onBack={onBack} />
      {isLoading ? (
        <ListSkeleton />
      ) : missing ? (
        <EmptyState>
          This connection has no glossary term with the URN{" "}
          <span className="break-all font-mono text-xs">{urn}</span>. It may belong to another
          connection, or have been retired since it was linked.
        </EmptyState>
      ) : isError ? (
        <Alert variant="destructive">
          <AlertDescription>Failed to load the linked glossary term.</AlertDescription>
        </Alert>
      ) : null}
    </div>
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
    <SectionCard title={<span className="text-lg font-semibold">Glossary</span>} action={create}>
      <GlossaryBranch
        branch={rootBranch(data)}
        isLoading={isLoading}
        isError={isError}
        emptyMessage="This connection has no glossary yet."
        onOpenNode={onOpenNode}
        onOpenTerm={onOpenTerm}
      />
    </SectionCard>
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
      <Button size="sm" onClick={() => onCreate("term")} aria-label={`New term ${where}`}>
        <Plus /> New term
      </Button>
      <Button
        variant="outline"
        size="sm"
        onClick={() => onCreate("node")}
        aria-label={`New node ${where}`}
      >
        <Plus /> New node
      </Button>
    </div>
  );
}
