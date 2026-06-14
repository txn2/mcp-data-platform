import type { SharePermission } from "@/api/portal/types";
import { cn } from "@/lib/utils";

/** Viewer / Editor pill shown on items shared with the current user. */
export function SharePermissionBadge({ permission }: { permission: SharePermission }) {
  const editor = permission === "editor";
  return (
    <span
      className={cn(
        "rounded-full px-1.5 py-0.5 text-xs font-medium",
        editor
          ? "bg-blue-100 text-blue-700 dark:bg-blue-950 dark:text-blue-300"
          : "bg-gray-100 text-gray-600 dark:bg-gray-800 dark:text-gray-400",
      )}
    >
      {editor ? "Editor" : "Viewer"}
    </span>
  );
}
