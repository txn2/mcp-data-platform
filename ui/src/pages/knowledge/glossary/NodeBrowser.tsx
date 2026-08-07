import { Folder } from "lucide-react";
import {
  useGlossaryChildren,
  useDeleteGlossaryEntity,
  type GlossaryChildren,
  type GlossaryNode,
  type GlossaryTerm,
} from "@/api/portal/datahub";
import { PageHeader } from "@/components/patterns/PageHeader";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { DeleteControl, EntityDescription } from "../catalog/governance";
import { shortUrn } from "../catalog/utils";
import { EntityDocuments, GlossaryBreadcrumb } from "../GlossaryDetail";
import { GlossaryBranch, nodeBranch } from "./GlossaryBranch";

// NodeBrowser is one node: where it sits, what it means, what is inside it, and
// the edits a steward can make from here.
export function NodeBrowser({
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
      <PageHeader
        backLabel="Back to the glossary"
        onBack={onOpenRoot}
        breadcrumb={
          <GlossaryBreadcrumb
            conn={conn}
            urn={node.urn}
            self={node.name || shortUrn(node.urn)}
            onOpenNode={onOpenNode}
            onOpenRoot={onOpenRoot}
          />
        }
        icon={Folder}
        title={node.name || shortUrn(node.urn)}
        urn={node.urn}
      />

      {canDelete && (
        <NodeDeleteControl
          conn={conn}
          node={node}
          contents={data}
          isError={isError}
          onDeleted={onOpenRoot}
        />
      )}

      <EntityDescription
        conn={conn}
        entity={node}
        canEdit={canEdit}
        label="Node definition"
        format="markdown"
      />

      <EntityDocuments conn={conn} urn={node.urn} />

      <SectionCard title="In this node" action={create}>
        <GlossaryBranch
          branch={nodeBranch(data)}
          isLoading={isLoading}
          isError={isError}
          emptyMessage="This node is empty."
          onOpenNode={onOpenNode}
          onOpenTerm={onOpenTerm}
        />
      </SectionCard>
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
  const notice = deleteNotice(contents, isError);
  if (notice) {
    return (
      <Alert>
        <AlertDescription>{notice}</AlertDescription>
      </Alert>
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

// deleteNotice is the reason the delete is not offered, or null when it is.
function deleteNotice(contents: GlossaryChildren | undefined, isError: boolean): string | null {
  if (isError) return "Could not read what is in this node, so its delete is not offered.";
  if (!contents) return "Checking what is in this node before offering to delete it.";
  if (contents.total > 0) {
    const noun = contents.total === 1 ? "entry" : "entries";
    return `This node holds ${contents.total} ${noun}. Move or delete them before deleting the node.`;
  }
  return null;
}
