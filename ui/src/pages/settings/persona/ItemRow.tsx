import { Check, Ban } from "lucide-react";
import { cn } from "@/lib/utils";
import type { Decision } from "./resolve";

// ItemRow renders one tool/connection in the permissions explorer, color-coded
// by its resolved decision, with hover affordances to allow/deny it by exact
// name. Extracted from PersonaEditor.tsx (#766).
export function ItemRow({
  name,
  secondary,
  tertiary,
  allowed,
  highlighted,
  highlightBucket,
  selected,
  decision,
  matchedPattern,
  onHover,
  onClick,
  onAddPattern,
}: {
  name: string;
  secondary: string;
  tertiary: string;
  allowed: boolean;
  highlighted: boolean;
  highlightBucket?: "allow" | "deny";
  selected: boolean;
  decision: Decision;
  matchedPattern: string;
  onHover: (h: boolean) => void;
  onClick: () => void;
  onAddPattern: (bucket: "allow" | "deny") => void;
}) {
  const statusBorder = allowed
    ? "border-l-emerald-500"
    : decision === "deny"
      ? "border-l-rose-500"
      : "border-l-muted-foreground/30";

  const bg = allowed
    ? "bg-gradient-to-r from-emerald-50/60 to-transparent dark:from-emerald-950/30"
    : decision === "deny"
      ? "bg-gradient-to-r from-rose-50/60 to-transparent dark:from-rose-950/30"
      : "bg-gradient-to-r from-muted/40 to-transparent";

  const ring = highlighted
    ? highlightBucket === "allow"
      ? "ring-2 ring-emerald-400"
      : "ring-2 ring-rose-400"
    : "";

  return (
    <div
      onMouseEnter={() => onHover(true)}
      onMouseLeave={() => onHover(false)}
      onClick={onClick}
      className={cn(
        "group relative cursor-pointer rounded-md border border-l-4 px-2.5 py-1.5 transition-all",
        statusBorder,
        bg,
        ring,
        selected ? "ring-2 ring-primary" : "hover:border-foreground/20",
      )}
    >
      <div className="flex items-center gap-2">
        <div className="flex h-4 w-4 shrink-0 items-center justify-center">
          {allowed ? (
            <Check className="h-3.5 w-3.5 text-emerald-600 dark:text-emerald-400" />
          ) : (
            <Ban className="h-3.5 w-3.5 text-rose-600 dark:text-rose-400" />
          )}
        </div>
        <div className="min-w-0 flex-1">
          <div className="truncate font-mono text-[11px] font-medium">
            {name}
          </div>
          <div className="mt-0.5 flex items-center gap-1.5 text-[10px] text-muted-foreground">
            <span>{secondary}</span>
            <span>·</span>
            <span>{tertiary}</span>
            {matchedPattern && (
              <>
                <span>·</span>
                <span className="font-mono italic">
                  matched {matchedPattern}
                </span>
              </>
            )}
          </div>
        </div>
        <div className="flex shrink-0 gap-0.5 opacity-0 transition-opacity group-hover:opacity-100">
          <button
            onClick={(e) => {
              e.stopPropagation();
              onAddPattern("allow");
            }}
            className="rounded p-1 text-emerald-700 hover:bg-emerald-100 dark:text-emerald-400 dark:hover:bg-emerald-950/60"
            title={`Allow ${name}`}
          >
            <Check className="h-3 w-3" />
          </button>
          <button
            onClick={(e) => {
              e.stopPropagation();
              onAddPattern("deny");
            }}
            className="rounded p-1 text-rose-700 hover:bg-rose-100 dark:text-rose-400 dark:hover:bg-rose-950/60"
            title={`Deny ${name}`}
          >
            <Ban className="h-3 w-3" />
          </button>
        </div>
      </div>
    </div>
  );
}
