import { cn } from "@/lib/utils";

const STATUS_STYLES: Record<string, string> = {
  draft: "bg-gray-100 text-gray-700 dark:bg-gray-800 dark:text-gray-300",
  approved: "bg-emerald-100 text-emerald-700 dark:bg-emerald-950 dark:text-emerald-300",
  deprecated: "bg-amber-100 text-amber-700 dark:bg-amber-950 dark:text-amber-300",
  superseded: "bg-rose-100 text-rose-700 dark:bg-rose-950 dark:text-rose-300",
};

// PromptStatusBadge renders a prompt's lifecycle status as a colored pill.
export function PromptStatusBadge({ status }: { status?: string }) {
  if (!status) return null;
  return (
    <span
      className={cn(
        "inline-flex items-center rounded-full px-2 py-0.5 text-[11px] font-medium capitalize",
        STATUS_STYLES[status] ?? STATUS_STYLES.draft,
      )}
    >
      {status}
    </span>
  );
}
