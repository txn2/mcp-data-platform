import { useId } from "react";

import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { cn } from "@/lib/utils";

// ---------------------------------------------------------------------------
// Small form helpers (shared across the catalog create/edit/spec surfaces)
// ---------------------------------------------------------------------------

// LabeledInput is one labelled text control: an ui/input tied to its ui/label
// by a generated id, with optional help text and an error line. Callers pass a
// label rather than an id so no catalog form has to mint one.
export function LabeledInput({
  label,
  help,
  value,
  onChange,
  placeholder,
  mono,
  disabled,
  invalid,
  error,
}: {
  label: string;
  help?: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  mono?: boolean;
  disabled?: boolean;
  invalid?: boolean;
  error?: string;
}) {
  const id = useId();
  const helpID = `${id}-help`;
  return (
    <div className="space-y-1.5">
      <Label htmlFor={id} className="text-xs">
        {label}
      </Label>
      <Input
        id={id}
        type="text"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        disabled={disabled}
        aria-invalid={invalid || undefined}
        aria-describedby={help || error ? helpID : undefined}
        className={cn(mono && "font-mono")}
      />
      {(help || error) && (
        <div id={helpID} className="space-y-1">
          {help && <p className="text-xs text-muted-foreground">{help}</p>}
          {error && <p className="text-xs text-destructive">{error}</p>}
        </div>
      )}
    </div>
  );
}

// LabeledTextarea is LabeledInput's multi-line counterpart, used for the
// description fields and the pasted OpenAPI document.
export function LabeledTextarea({
  label,
  help,
  value,
  onChange,
  placeholder,
  rows,
  mono,
}: {
  label: string;
  help?: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  rows?: number;
  mono?: boolean;
}) {
  const id = useId();
  const helpID = `${id}-help`;
  return (
    <div className="space-y-1.5">
      <Label htmlFor={id} className="text-xs">
        {label}
      </Label>
      <Textarea
        id={id}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        rows={rows ?? 3}
        aria-describedby={help ? helpID : undefined}
        // ui/textarea sizes to its content, which would shrink the paste box
        // to the height of its placeholder. `rows` is the caller stating how
        // much room the field needs up front, so honour it.
        className={cn("field-sizing-fixed", mono && "font-mono")}
      />
      {help && (
        <p id={helpID} className="text-xs text-muted-foreground">
          {help}
        </p>
      )}
    </div>
  );
}
