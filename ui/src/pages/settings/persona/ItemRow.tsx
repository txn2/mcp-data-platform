import { Check, Ban } from "lucide-react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { BUCKET_TINT, type Bucket } from "./tints";
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
  highlightBucket?: Bucket;
  selected: boolean;
  decision: Decision;
  matchedPattern: string;
  onHover: (h: boolean) => void;
  onClick: () => void;
  onAddPattern: (bucket: Bucket) => void;
}) {
  // A row reads its own verdict: allowed and denied carry the bucket tint,
  // while "no pattern matched" is untinted — nothing decided it.
  const verdict: Bucket | null = allowed ? "allow" : decision === "deny" ? "deny" : null;
  const statusBorder = verdict
    ? BUCKET_TINT[verdict].edge
    : "border-l-muted-foreground/30";
  const surface = verdict
    ? BUCKET_TINT[verdict].surface
    : "bg-gradient-to-r from-muted/40 to-transparent";

  return (
    <div
      onMouseEnter={() => onHover(true)}
      onMouseLeave={() => onHover(false)}
      onClick={onClick}
      className={cn(
        "group relative cursor-pointer rounded-md border border-l-4 px-2.5 py-1.5 transition-all",
        statusBorder,
        surface,
        highlighted && highlightBucket && BUCKET_TINT[highlightBucket].ring,
        selected ? "ring-2 ring-primary" : "hover:border-foreground/20",
      )}
    >
      <div className="flex items-center gap-2">
        <div className="flex h-4 w-4 shrink-0 items-center justify-center">
          {allowed ? (
            <Check className={cn("h-3.5 w-3.5", BUCKET_TINT.allow.icon)} />
          ) : (
            <Ban className={cn("h-3.5 w-3.5", BUCKET_TINT.deny.icon)} />
          )}
        </div>
        <div className="min-w-0 flex-1">
          <div className="truncate font-mono text-[11px] font-medium">{name}</div>
          <div className="mt-0.5 flex items-center gap-1.5 text-[10px] text-muted-foreground">
            <span>{secondary}</span>
            <span>·</span>
            <span>{tertiary}</span>
            {matchedPattern && (
              <>
                <span>·</span>
                <span className="font-mono italic">matched {matchedPattern}</span>
              </>
            )}
          </div>
        </div>
        <div className="flex shrink-0 gap-0.5 opacity-0 transition-opacity group-hover:opacity-100">
          <Button
            type="button"
            variant="ghost"
            size="icon-xs"
            onClick={(e) => {
              e.stopPropagation();
              onAddPattern("allow");
            }}
            aria-label={`Allow ${name}`}
            title={`Allow ${name}`}
            className={BUCKET_TINT.allow.action}
          >
            <Check />
          </Button>
          <Button
            type="button"
            variant="ghost"
            size="icon-xs"
            onClick={(e) => {
              e.stopPropagation();
              onAddPattern("deny");
            }}
            aria-label={`Deny ${name}`}
            title={`Deny ${name}`}
            className={BUCKET_TINT.deny.action}
          >
            <Ban />
          </Button>
        </div>
      </div>
    </div>
  );
}
