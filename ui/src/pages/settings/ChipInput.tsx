import { X } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";

// ChipInput is the one way the settings area edits a free-form list of short
// strings — persona roles, API-key roles. Values commit on Enter, comma, or
// blur, and Backspace on an empty draft removes the last one, so a list can be
// typed without reaching for the mouse.
export function ChipInput({
  values,
  onAdd,
  onRemove,
  draft,
  onDraftChange,
  placeholder,
  label,
}: {
  values: string[];
  onAdd: (v: string) => void;
  onRemove: (v: string) => void;
  draft: string;
  onDraftChange: (s: string) => void;
  placeholder?: string;
  // The text entry's accessible name. The box around it is not a labelable
  // element, so the name has to live on the input itself.
  label?: string;
}) {
  return (
    <div className="flex flex-wrap gap-1 rounded-md border bg-background p-1.5 focus-within:ring-2 focus-within:ring-ring">
      {values.map((v) => (
        <Badge key={v} variant="muted" className="gap-1 rounded font-mono text-[10px]">
          {v}
          <Button
            type="button"
            variant="ghost"
            size="icon-xs"
            onClick={() => onRemove(v)}
            aria-label={`remove ${v}`}
            className="size-3.5 text-muted-foreground hover:bg-transparent hover:text-foreground"
          >
            <X className="size-2.5" />
          </Button>
        </Badge>
      ))}
      {/* The text entry is the chip box's own interior, not a standalone
          control: it has no border, no height, and no background of its own,
          because the bordered box around the chips is the input. */}
      <input
        type="text"
        value={draft}
        aria-label={label}
        onChange={(e) => onDraftChange(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter" || e.key === ",") {
            e.preventDefault();
            if (draft.trim()) onAdd(draft);
          } else if (e.key === "Backspace" && !draft && values.length > 0) {
            onRemove(values[values.length - 1]!);
          }
        }}
        onBlur={() => {
          if (draft.trim()) onAdd(draft);
        }}
        placeholder={values.length === 0 ? placeholder : ""}
        className="min-w-[80px] flex-1 bg-transparent text-[11px] outline-none placeholder:text-muted-foreground"
      />
    </div>
  );
}
