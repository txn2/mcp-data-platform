import type { LucideIcon } from "lucide-react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

export interface SegmentedOption<T extends string> {
  value: T;
  /** The option's accessible name, and its hover copy. */
  label: string;
  /** The option's icon, shown before `text` or on its own. */
  icon?: LucideIcon;
  /** The option's visible words, e.g. "L" for "Large". */
  text?: string;
}

// SegmentedControl is the one shape a small "which way do I want this shown"
// switch takes: adjacent ui/button faces in a bordered trough, the chosen one
// filled. It is a set of toggles rather than a tablist because nothing below it
// is a tab panel — the same content is being redrawn, so each face states its
// own pressed status and keeps its button role.
export function SegmentedControl<T extends string>({
  label,
  value,
  onChange,
  options,
  className,
}: {
  /** Names the group for assistive tech, e.g. "Thumbnail size". */
  label: string;
  value: T;
  onChange: (value: T) => void;
  options: SegmentedOption<T>[];
  className?: string;
}) {
  return (
    <div
      role="group"
      aria-label={label}
      className={cn("flex shrink-0 gap-0.5 rounded-md border p-0.5", className)}
    >
      {options.map((opt) => {
        const Icon = opt.icon;
        const active = opt.value === value;
        // An icon with no words needs the label as its accessible name; with
        // words, the words are the name and the label is only hover copy.
        const iconOnly = !!Icon && !opt.text;
        return (
          <Button
            key={opt.value}
            type="button"
            variant={active ? "secondary" : "ghost"}
            size={iconOnly ? "icon-sm" : "xs"}
            aria-pressed={active}
            aria-label={iconOnly ? opt.label : undefined}
            title={opt.label}
            onClick={() => onChange(opt.value)}
            className={cn(!active && "text-muted-foreground")}
          >
            {Icon && <Icon />}
            {opt.text}
          </Button>
        );
      })}
    </div>
  );
}
