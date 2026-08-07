import { AlertCircle } from "lucide-react";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import { cn } from "@/lib/utils";

// The pieces every prompt view repeats: a labelled form field, a save/load
// failure banner, and the placeholder a list shows while it loads. They live
// here so the library, the viewer, and the admin table state each of them
// once. The sortable column header moved to the shared patterns (#1207) once
// the audit events table needed the same header; it is re-exported so prompt
// views keep importing it from here.
export { SortableHead } from "@/components/patterns/SortableHead";

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
