import { useState, useCallback, useId } from "react";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { cn } from "@/lib/utils";

// ---------------------------------------------------------------------------
// Field + JSONField primitives
// ---------------------------------------------------------------------------

export function Field({
  label,
  hint,
  htmlFor,
  children,
}: {
  label: string;
  hint?: string;
  // The id of the control this labels. Omitted where the caller renders its
  // own labelled control inside.
  htmlFor?: string;
  children: React.ReactNode;
}) {
  return (
    <div>
      <div className="mb-1 flex items-baseline justify-between gap-2">
        <Label
          htmlFor={htmlFor}
          className="text-xs font-semibold uppercase tracking-wider text-muted-foreground"
        >
          {label}
        </Label>
        {hint && <span className="text-xs text-muted-foreground">{hint}</span>}
      </div>
      {children}
    </div>
  );
}

export function JSONField<T>({
  label,
  hint,
  value,
  onChange,
}: {
  label: string;
  hint?: string;
  value: T;
  onChange: (v: T) => void;
}) {
  const id = useId();
  const [text, setText] = useState(() => JSON.stringify(value, null, 2));
  const [error, setError] = useState<string | null>(null);

  const handleChange = useCallback(
    (next: string) => {
      setText(next);
      try {
        onChange(JSON.parse(next) as T);
        setError(null);
      } catch (err) {
        setError(err instanceof Error ? err.message : "Invalid JSON");
      }
    },
    [onChange],
  );

  return (
    <Field label={label} hint={hint} htmlFor={id}>
      {/* field-sizing-fixed: ui/textarea grows to its content by default,
          which would override the five rows this editor asks for. */}
      <Textarea
        id={id}
        rows={5}
        value={text}
        onChange={(e) => handleChange(e.target.value)}
        aria-invalid={error !== null}
        className={cn("field-sizing-fixed min-h-0 px-2 py-1 font-mono text-xs")}
      />
      {error && <p className="mt-1 text-xs text-destructive">{error}</p>}
    </Field>
  );
}
