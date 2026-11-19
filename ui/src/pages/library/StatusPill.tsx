import { cn } from "@/lib/utils";

const STYLES: Record<string, string> = {
  draft: "bg-muted text-muted-foreground",
  published: "bg-emerald-100 text-emerald-800 dark:bg-emerald-950 dark:text-emerald-200",
  deprecated: "bg-orange-100 text-orange-800 dark:bg-orange-950 dark:text-orange-200",
};

const LABELS: Record<string, string> = { draft: "Draft", published: "Published", deprecated: "Deprecated" };

/** StatusPill shows an asset's status (#2502) in a library list; none reads "none". */
export function StatusPill({ status }: { status?: string }) {
  if (!status || !LABELS[status]) return <span className="text-muted-foreground">none</span>;
  return <span className={cn("rounded-full px-2 py-0.5 text-xs", STYLES[status])}>{LABELS[status]}</span>;
}
