import { useState, useCallback } from "react";
import { cn } from "@/lib/utils";

// ---------------------------------------------------------------------------
// Field + JSONField primitives
// ---------------------------------------------------------------------------

export function Field({
  label,
  hint,
  children,
}: {
  label: string;
  hint?: string;
  children: React.ReactNode;
}) {
  return (
    <div>
      <div className="mb-1 flex items-baseline justify-between gap-2">
        <label className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
          {label}
        </label>
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
    <Field label={label} hint={hint}>
      <textarea
        className={cn(
          "w-full rounded-md border bg-background px-2 py-1 text-xs font-mono",
          error && "border-destructive",
        )}
        rows={5}
        value={text}
        onChange={(e) => handleChange(e.target.value)}
      />
      {error && <p className="mt-1 text-xs text-destructive">{error}</p>}
    </Field>
  );
}
