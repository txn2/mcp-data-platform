import { File, Search, Users } from "lucide-react";
import { EmptyState } from "@/components/patterns/EmptyState";
import type { Scope } from "@/components/ScopeFilter";

/**
 * What an empty Assets list says, which depends on why it is empty: a query
 * that matched nothing, a scope nobody has shared into, or an account that has
 * not saved an asset yet.
 */
export function AssetsEmpty({
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
        <p className="font-medium">No assets match &ldquo;{query}&rdquo;</p>
      </EmptyState>
    );
  }
  if (scope === "shared") {
    return (
      <EmptyState icon={Users}>
        <p className="font-medium">No shared assets</p>
        <p className="text-xs">Assets others share with you will appear here.</p>
      </EmptyState>
    );
  }
  if (scope === "all") {
    return (
      <EmptyState icon={File}>
        <p className="font-medium">No assets</p>
      </EmptyState>
    );
  }
  // #1015: the moment someone has nowhere to put a file they wrote themselves
  // is the moment to say assets are not that place.
  return (
    <EmptyState icon={File}>
      <p className="font-medium">No assets yet</p>
      <div className="mx-auto mt-3 max-w-md space-y-2 text-xs">
        <p>
          Assets are interactive dashboards, visualizations, and documents created during your
          conversations.
        </p>
        <p>
          Try asking your assistant to <em>"create an interactive dashboard"</em> or{" "}
          <em>"save this as an asset"</em> to get started.
        </p>
        <p>A file you wrote yourself and want used as-is belongs in Resources, not here.</p>
      </div>
    </EmptyState>
  );
}
