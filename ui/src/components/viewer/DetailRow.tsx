import type { ReactNode } from "react";

/**
 * One label/value line in a viewer sidebar's details list.
 *
 * Written once for both kinds the sidebar serves (#1470): a portal asset's
 * type, size and dates read the same way a managed resource's do, and a row
 * that drifted in one would show as an unexplained difference between two
 * pages that are otherwise the same page.
 */
export function DetailRow({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex justify-between gap-2">
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="min-w-0">{children}</dd>
    </div>
  );
}
