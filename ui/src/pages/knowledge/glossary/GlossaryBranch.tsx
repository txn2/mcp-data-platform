import { BookMarked, Folder } from "lucide-react";
import type {
  GlossaryChildren,
  GlossaryNode,
  GlossaryRoots,
  GlossaryTerm,
} from "@/api/portal/datahub";
import { EmptyState } from "@/components/patterns/EmptyState";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { ListSkeleton } from "../catalog/primitives";
import { VocabCard } from "../catalog/governance";

// Branch is one level of the tree in the shape the renderer needs, which is not
// the shape either read returns: the roots read pages nodes and terms with a
// total each, a node's children come back as one mixed page with one total.
// rootBranch and nodeBranch reconcile them so the renderer has a single case.
export interface Branch {
  nodes: GlossaryNode[];
  terms: GlossaryTerm[];
  total: number;
}

const EMPTY_BRANCH: Branch = { nodes: [], terms: [], total: 0 };

export function rootBranch(data: GlossaryRoots | undefined): Branch {
  if (!data) return EMPTY_BRANCH;
  return { nodes: data.nodes, terms: data.terms, total: data.nodes_total + data.terms_total };
}

export function nodeBranch(data: GlossaryChildren | undefined): Branch {
  if (!data) return EMPTY_BRANCH;
  return { nodes: data.nodes, terms: data.terms, total: data.total };
}

// GlossaryBranch renders one level of the tree: its nodes first, then its terms,
// so the structure reads before the vocabulary inside it.
export function GlossaryBranch({
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
      <Alert variant="destructive">
        <AlertDescription>Failed to load the glossary.</AlertDescription>
      </Alert>
    );
  }
  if (isLoading) return <ListSkeleton />;
  if (shown === 0) {
    return <EmptyState>{emptyMessage}</EmptyState>;
  }

  return (
    <div className="space-y-2">
      {/* The total is the backend's, not this page's: a branch wider than one
          page says how much it is not showing rather than presenting the page
          as the whole branch. */}
      {shown < total && (
        <p className="text-xs text-muted-foreground">
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
