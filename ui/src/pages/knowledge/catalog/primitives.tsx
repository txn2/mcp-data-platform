import { Pencil, Plus, AlertCircle } from "lucide-react";
import { ApiError } from "@/api/portal/client";

export function Badge({ tone, children }: { tone: "primary" | "amber"; children: React.ReactNode }) {
  const cls =
    tone === "amber"
      ? "bg-amber-500/10 text-amber-600 dark:text-amber-400"
      : "bg-primary/10 text-primary";
  return <span className={`rounded px-1.5 py-0.5 text-[11px] ${cls}`}>{children}</span>;
}

export function ListSkeleton() {
  return (
    <div className="grid gap-2 sm:grid-cols-2">
      {Array.from({ length: 6 }).map((_, i) => (
        <div key={i} className="h-16 animate-pulse rounded-lg border bg-muted/40" />
      ))}
    </div>
  );
}

export function EditButton({ onClick }: { onClick: () => void }) {
  return (
    <button
      onClick={onClick}
      className="inline-flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground"
    >
      <Pencil className="h-3.5 w-3.5" /> Edit
    </button>
  );
}

export function SaveButton({ onClick, disabled }: { onClick: () => void; disabled?: boolean }) {
  return (
    <button
      onClick={onClick}
      disabled={disabled}
      className="rounded-md bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
    >
      Save
    </button>
  );
}

export function CancelButton({ onClick }: { onClick: () => void }) {
  return (
    <button onClick={onClick} className="rounded-md border px-3 py-1.5 text-sm hover:bg-muted">
      Cancel
    </button>
  );
}

export function AddButton({
  onClick,
  disabled,
  label = "Add",
}: {
  onClick: () => void;
  disabled?: boolean;
  label?: string;
}) {
  return (
    <button
      onClick={onClick}
      disabled={disabled}
      className="inline-flex items-center gap-1 rounded-md border px-2 py-1 text-xs font-medium hover:bg-muted disabled:opacity-50"
    >
      <Plus className="h-3 w-3" /> {label}
    </button>
  );
}

export function MutationError({ mut }: { mut: { isError: boolean; error: unknown } }) {
  if (!mut.isError) return null;
  const msg = mut.error instanceof ApiError ? mut.error.detail : "Update failed.";
  return (
    <p
      role="alert"
      className="mt-2 flex items-start gap-1.5 rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive"
    >
      <AlertCircle className="mt-0.5 h-4 w-4 shrink-0" />
      <span>{msg}</span>
    </p>
  );
}
