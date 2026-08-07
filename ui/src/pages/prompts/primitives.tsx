import { AlertCircle, ChevronDown, ChevronsUpDown, ChevronUp } from "lucide-react";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import { TableHead } from "@/components/ui/table";
import { cn } from "@/lib/utils";

// The pieces every prompt view repeats: a sortable column header, a labelled
// form field, a save/load failure banner, and the placeholder a list shows
// while it loads. They live here so the library, the viewer, and the admin
// table state each of them once.

export function SortableHead<K extends string>({
  label,
  sortKey,
  sortBy,
  sortDir,
  onSort,
  className,
}: {
  label: string;
  sortKey: K;
  sortBy: K;
  sortDir: "asc" | "desc";
  onSort: (key: K) => void;
  className?: string;
}) {
  const active = sortBy === sortKey;
  const Chevron = active ? (sortDir === "asc" ? ChevronUp : ChevronDown) : ChevronsUpDown;
  return (
    <TableHead
      onClick={() => onSort(sortKey)}
      className={cn(
        "cursor-pointer text-muted-foreground select-none hover:bg-muted/80",
        className,
      )}
    >
      <span className="inline-flex items-center gap-1">
        {label}
        <Chevron className={cn("size-3", active ? "text-foreground" : "text-muted-foreground/50")} />
      </span>
    </TableHead>
  );
}

// Field is one labelled control. The caller owns the control so it can be an
// Input, a Textarea, a Select, or an editor; the id ties it to the label.
export function Field({
  id,
  label,
  hint,
  className,
  children,
}: {
  id: string;
  label: string;
  // Helper or validation text under the control.
  hint?: React.ReactNode;
  className?: string;
  children: React.ReactNode;
}) {
  return (
    <div className={cn("space-y-1.5", className)}>
      <Label htmlFor={id} className="text-xs text-muted-foreground">
        {label}
      </Label>
      {children}
      {hint}
    </div>
  );
}

// FormError is the one shape a failed save takes across the prompt forms.
export function FormError({ message }: { message?: string | null }) {
  if (!message) return null;
  return (
    <Alert variant="destructive">
      <AlertCircle />
      <AlertDescription>{message}</AlertDescription>
    </Alert>
  );
}

export function ListSkeleton({ rows = 5 }: { rows?: number }) {
  return (
    <div className="space-y-2">
      {Array.from({ length: rows }).map((_, i) => (
        <Skeleton key={i} className="h-12 rounded-lg" />
      ))}
    </div>
  );
}
