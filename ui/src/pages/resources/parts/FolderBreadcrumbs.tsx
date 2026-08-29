import { ChevronRight } from "lucide-react";
import { cn } from "@/lib/utils";
import { segments } from "./tree";

/**
 * Where in a library something is, as a trail from the library down.
 *
 * One component for the library's own header and the resource viewer's subtitle
 * (#1530). Both were printing a path -- the viewer as three words with no way to
 * act on them -- and a reader who can see the folder chain should be able to
 * click any level of it.
 */
export function FolderBreadcrumbs({
  library,
  path,
  onOpen,
  className,
}: {
  /** What the library itself is called: "My Resources", "Global", a persona. */
  library: string;
  /** The folder path inside it, "" at the library's root. */
  path: string;
  /**
   * Opens one level. Absent makes the trail plain text, which is what a viewer
   * with nowhere to send the reader shows.
   */
  onOpen?: (path: string) => void;
  className?: string;
}) {
  const parts = segments(path);
  const crumbs = [
    { label: library, at: "" },
    ...parts.map((name, i) => ({ label: name, at: parts.slice(0, i + 1).join("/") })),
  ];

  return (
    // A span carrying the navigation role rather than a <nav>: the resource
    // viewer renders this inside its subtitle, which is a <p>, and a <nav>
    // nested in a paragraph is invalid HTML the browser reparents.
    <span
      role="navigation"
      aria-label="Folder path"
      className={cn("flex min-w-0 items-center gap-1", className)}
    >
      {crumbs.map((crumb, i) => {
        // The last crumb is where the reader already is, so it is not a control:
        // an enabled one that navigates nowhere reads as a broken link.
        const here = i === crumbs.length - 1;
        return (
          <span key={crumb.at || "root"} className="flex min-w-0 items-center gap-1">
            {i > 0 && <ChevronRight aria-hidden className="size-3 shrink-0 text-muted-foreground" />}
            {here || !onOpen ? (
              <span
                className={cn("truncate", here ? "font-medium text-foreground" : "text-muted-foreground")}
                aria-current={here ? "page" : undefined}
              >
                {crumb.label}
              </span>
            ) : (
              <button
                type="button"
                onClick={() => onOpen(crumb.at)}
                className="truncate text-muted-foreground underline-offset-2 hover:text-foreground hover:underline"
              >
                {crumb.label}
              </button>
            )}
          </span>
        );
      })}
    </span>
  );
}
