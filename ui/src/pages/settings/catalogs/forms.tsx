import { cn } from "@/lib/utils";

// ---------------------------------------------------------------------------
// Small form helpers (shared across the catalog create/edit/spec surfaces)
// ---------------------------------------------------------------------------

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
  return (
    <div>
      <label className="mb-1 block text-xs font-medium">{label}</label>
      <input
        type="text"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        disabled={disabled}
        className={cn(
          "w-full rounded-md border bg-background px-3 py-2 text-sm outline-none ring-ring focus:ring-2 disabled:opacity-60",
          mono && "font-mono",
          invalid && "border-destructive",
        )}
      />
      {help && <p className="mt-1 text-xs text-muted-foreground">{help}</p>}
      {error && <p className="mt-1 text-xs text-destructive">{error}</p>}
    </div>
  );
}

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
  return (
    <div>
      <label className="mb-1 block text-xs font-medium">{label}</label>
      <textarea
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        rows={rows ?? 3}
        className={cn(
          "w-full rounded-md border bg-background px-3 py-2 text-sm outline-none ring-ring focus:ring-2",
          mono && "font-mono",
        )}
      />
      {help && <p className="mt-1 text-xs text-muted-foreground">{help}</p>}
    </div>
  );
}
