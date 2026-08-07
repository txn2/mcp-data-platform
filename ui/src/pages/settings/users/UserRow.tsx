import { useCallback, useState } from "react";
import { Check, Pencil, Trash2, X } from "lucide-react";
import { useUpdateUser } from "@/api/admin/hooks";
import type { DirectoryUser } from "@/api/admin/types";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { TableCell, TableRow } from "@/components/ui/table";

function fullName(u: DirectoryUser): string {
  const name = [u.first_name, u.last_name].filter(Boolean).join(" ");
  return name || "--";
}

function formatSeen(at?: string): string {
  if (!at) return "Never";
  return new Date(at).toLocaleDateString();
}

// UserStatus says whether the person has signed in yet. "Invited" is not a
// failure state -- it is a directory entry waiting on its owner -- so it is
// warning-tinted rather than danger-tinted.
function UserStatus({ user }: { user: DirectoryUser }) {
  if (user.confirmed) {
    return <Badge variant="success">Active</Badge>;
  }
  return (
    <Badge
      variant="warning"
      title={user.added_by ? `Added by ${user.added_by}` : undefined}
    >
      Invited
    </Badge>
  );
}

export interface UserRowProps {
  user: DirectoryUser;
  isReadOnly: boolean;
  deleteConfirm: boolean;
  deleting: boolean;
  onRequestDelete: () => void;
  onCancelDelete: () => void;
  onConfirmDelete: () => void;
}

// UserRow is one directory entry, in view or inline-edit mode. Extracted from
// UsersPanel.tsx (#1206).
export function UserRow({
  user,
  isReadOnly,
  deleteConfirm,
  deleting,
  onRequestDelete,
  onCancelDelete,
  onConfirmDelete,
}: UserRowProps) {
  const [editing, setEditing] = useState(false);
  const [first, setFirst] = useState(user.first_name);
  const [last, setLast] = useState(user.last_name);
  const updateMutation = useUpdateUser();

  const save = useCallback(() => {
    updateMutation.mutate(
      { email: user.email, first_name: first, last_name: last },
      { onSuccess: () => setEditing(false) },
    );
  }, [updateMutation, user.email, first, last]);

  if (editing) {
    return (
      <TableRow className="bg-muted/20">
        <TableCell className="px-5 py-2" colSpan={2}>
          <div className="flex gap-2">
            <Input
              value={first}
              onChange={(e) => setFirst(e.target.value)}
              placeholder="First name"
              aria-label="First name"
              className="h-8 w-32"
            />
            <Input
              value={last}
              onChange={(e) => setLast(e.target.value)}
              placeholder="Last name"
              aria-label="Last name"
              className="h-8 w-32"
            />
            <span className="self-center text-xs text-muted-foreground">
              {user.email}
            </span>
          </div>
        </TableCell>
        <TableCell className="px-5 py-2" colSpan={3}>
          <div className="flex items-center gap-1.5">
            <Button
              type="button"
              size="xs"
              onClick={save}
              disabled={updateMutation.isPending}
            >
              <Check />
              {updateMutation.isPending ? "Saving..." : "Save"}
            </Button>
            <Button
              type="button"
              variant="outline"
              size="icon-xs"
              aria-label="Cancel edit"
              onClick={() => {
                setFirst(user.first_name);
                setLast(user.last_name);
                setEditing(false);
              }}
            >
              <X />
            </Button>
          </div>
        </TableCell>
      </TableRow>
    );
  }

  return (
    <TableRow>
      <TableCell className="px-5 py-3 font-medium">{fullName(user)}</TableCell>
      <TableCell className="px-5 py-3 text-muted-foreground">{user.email}</TableCell>
      <TableCell className="px-5 py-3">
        <UserStatus user={user} />
      </TableCell>
      <TableCell className="px-5 py-3 text-muted-foreground">
        {formatSeen(user.last_seen_at)}
      </TableCell>
      {!isReadOnly && (
        <TableCell className="px-5 py-3">
          {deleteConfirm ? (
            <div className="flex items-center gap-1.5">
              <Button
                type="button"
                variant="destructive"
                size="xs"
                onClick={onConfirmDelete}
                disabled={deleting}
              >
                {deleting ? "..." : "Confirm"}
              </Button>
              <Button
                type="button"
                variant="outline"
                size="icon-xs"
                aria-label={`Cancel deleting ${user.email}`}
                onClick={onCancelDelete}
              >
                <X />
              </Button>
            </div>
          ) : (
            <div className="flex items-center gap-1">
              <Button
                type="button"
                variant="ghost"
                size="xs"
                className="text-muted-foreground"
                onClick={() => {
                  // Seed the inputs from the current props at edit time, not at
                  // mount, so a row whose data was refetched while displayed
                  // does not prefill stale names.
                  setFirst(user.first_name);
                  setLast(user.last_name);
                  setEditing(true);
                }}
              >
                <Pencil />
                Edit
              </Button>
              <Button
                type="button"
                variant="ghost"
                size="icon-xs"
                aria-label={`Delete ${user.email}`}
                onClick={onRequestDelete}
                className="text-muted-foreground hover:bg-destructive/10 hover:text-destructive"
              >
                <Trash2 />
              </Button>
            </div>
          )}
        </TableCell>
      )}
    </TableRow>
  );
}
