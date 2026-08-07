import { FolderOpen, Search, Users } from "lucide-react";
import { EmptyState } from "@/components/patterns/EmptyState";
import type { Scope } from "@/components/ScopeFilter";

/** What an empty Collections list says, phrased for why it is empty. */
export function CollectionsEmpty({
  scope,
  searching,
  query,
}: {
  scope: Scope;
  searching: boolean;
  query: string;
}) {
  if (searching) {
    return (
      <EmptyState icon={Search}>
        <p className="font-medium">No collections match &ldquo;{query}&rdquo;</p>
      </EmptyState>
    );
  }
  if (scope === "shared") {
    return (
      <EmptyState icon={Users}>
        <p className="font-medium">No shared collections</p>
        <p className="text-xs">Collections others share with you will appear here.</p>
      </EmptyState>
    );
  }
  return (
    <EmptyState icon={FolderOpen}>
      <p className="font-medium">No collections yet</p>
      <p className="text-xs">Create a collection to organize your assets into curated groups.</p>
    </EmptyState>
  );
}
