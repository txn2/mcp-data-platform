import { Pencil, Plus, AlertCircle } from "lucide-react";
import { ApiError } from "@/api/portal/client";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";

export function ListSkeleton() {
  return (
    <div className="grid gap-2 sm:grid-cols-2">
      {Array.from({ length: 6 }).map((_, i) => (
        <Skeleton key={i} className="h-16 rounded-lg" />
      ))}
    </div>
  );
}

export function EditButton({ onClick }: { onClick: () => void }) {
  return (
    <Button variant="ghost" size="xs" onClick={onClick} className="text-muted-foreground">
      <Pencil /> Edit
    </Button>
  );
}

export function SaveButton({ onClick, disabled }: { onClick: () => void; disabled?: boolean }) {
  return (
    <Button size="sm" onClick={onClick} disabled={disabled}>
      Save
    </Button>
  );
}

export function CancelButton({ onClick }: { onClick: () => void }) {
  return (
    <Button variant="outline" size="sm" onClick={onClick}>
      Cancel
    </Button>
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
    <Button variant="outline" size="xs" onClick={onClick} disabled={disabled}>
      <Plus /> {label}
    </Button>
  );
}

export function MutationError({ mut }: { mut: { isError: boolean; error: unknown } }) {
  if (!mut.isError) return null;
  const msg = mut.error instanceof ApiError ? mut.error.detail : "Update failed.";
  return (
    <Alert variant="destructive" className="mt-2">
      <AlertCircle />
      <AlertDescription>{msg}</AlertDescription>
    </Alert>
  );
}
