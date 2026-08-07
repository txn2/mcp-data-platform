import { X } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { matchPattern } from "./resolve";
import { renderPattern } from "./renderPattern";

interface RuleListItem {
  key: string;
  primary: string;
}

// RuleList renders the editable allow/deny pattern chips for one bucket, with
// a live match count and hover highlight wired back to the explorer. Extracted
// from PersonaEditor.tsx (#766).
export function RuleList({
  bucket,
  patterns,
  items,
  highlightRule,
  onHover,
  onRemove,
}: {
  bucket: "allow" | "deny";
  patterns: string[];
  items: RuleListItem[];
  highlightRule: { bucket: "allow" | "deny"; pattern: string } | null;
  onHover: (p: string | null) => void;
  onRemove: (p: string) => void;
}) {
  if (patterns.length === 0) {
    return (
      <p className="text-[11px] italic text-muted-foreground">No patterns.</p>
    );
  }
  return (
    <div className="space-y-1">
      {patterns.map((p) => {
        const matches = items.filter((it) => matchPattern(p, it.primary)).length;
        const isHovered =
          highlightRule?.bucket === bucket && highlightRule.pattern === p;
        return (
          <Badge
            key={p}
            // A rule chip is the bucket's own verdict rendered on a pattern,
            // so it carries the same success/danger tint the resolved items
            // do — widened to a row because it also holds a count and a
            // remove control.
            variant={bucket === "allow" ? "success" : "danger"}
            onMouseEnter={() => onHover(p)}
            onMouseLeave={() => onHover(null)}
            className={cn(
              "group flex w-full items-center gap-2 rounded px-2 py-1",
              isHovered && "ring-1 ring-offset-1 ring-offset-background",
            )}
          >
            <span className="flex-1 truncate font-mono text-[11px]">
              {renderPattern(p)}
            </span>
            <span className="font-mono text-[10px] opacity-70">{matches}</span>
            <Button
              type="button"
              variant="ghost"
              size="icon-xs"
              onClick={() => onRemove(p)}
              aria-label={`remove ${p}`}
              className="size-4 opacity-0 transition-opacity hover:bg-background/60 group-hover:opacity-100"
            >
              <X />
            </Button>
          </Badge>
        );
      })}
    </div>
  );
}
