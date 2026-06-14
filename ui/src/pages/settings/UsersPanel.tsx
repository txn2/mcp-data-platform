import { useState, useCallback } from "react";
import {
  useDirectoryUsers,
  useCreateUser,
  useUpdateUser,
  useDeleteUser,
  useSystemInfo,
} from "@/api/admin/hooks";
import type { DirectoryUser } from "@/api/admin/types";
import { cn } from "@/lib/utils";
import { Plus, Trash2, X, Contact, ChevronUp, Pencil, Check, Search } from "lucide-react";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function fullName(u: DirectoryUser): string {
  const name = [u.first_name, u.last_name].filter(Boolean).join(" ");
  return name || "--";
}

function formatSeen(at?: string): string {
  if (!at) return "Never";
  return new Date(at).toLocaleDateString();
}

// ---------------------------------------------------------------------------
// UsersPanel — directory of known people (#614)
// ---------------------------------------------------------------------------

export function UsersPanel() {
  const { data: systemInfo } = useSystemInfo();
  const isReadOnly = systemInfo?.config_mode === "file";

  const [query, setQuery] = useState("");
  const { data: userList, isLoading } = useDirectoryUsers(query.trim() || undefined);
  const users = userList?.users ?? [];

  const [showForm, setShowForm] = useState(false);
  const [deleteConfirm, setDeleteConfirm] = useState<string | null>(null);
  const deleteMutation = useDeleteUser();

  const handleDelete = useCallback(
    (email: string) => {
      deleteMutation.mutate(email, { onSuccess: () => setDeleteConfirm(null) });
    },
    [deleteMutation],
  );

  return (
    <div className="flex h-[calc(100vh-8rem)] flex-col overflow-hidden rounded-lg border bg-card">
      {/* Header */}
      <div className="flex items-start justify-between gap-4 border-b px-5 py-3">
        <div className="min-w-0">
          <h3 className="text-sm font-semibold leading-none">Users</h3>
          <p className="mt-1 text-xs text-muted-foreground">
            People known to the platform, for sharing assets and prompts. Anyone who signs in is
            recorded automatically; add others by email so they can be shared with before they log in.
          </p>
        </div>
        {!isReadOnly && (
          <button
            type="button"
            onClick={() => setShowForm((prev) => !prev)}
            className={cn(
              "inline-flex shrink-0 items-center gap-1.5 rounded-md px-3 py-1.5 text-xs font-medium transition-colors",
              showForm
                ? "border text-muted-foreground hover:bg-muted"
                : "bg-primary text-primary-foreground hover:bg-primary/90",
            )}
          >
            {showForm ? (
              <>
                <ChevronUp className="h-3.5 w-3.5" />
                Cancel
              </>
            ) : (
              <>
                <Plus className="h-3.5 w-3.5" />
                Add User
              </>
            )}
          </button>
        )}
      </div>

      {/* Add user form (slide-down) */}
      {showForm && <AddUserForm onDone={() => setShowForm(false)} />}

      {/* Search */}
      <div className="border-b px-5 py-2">
        <div className="relative max-w-sm">
          <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
          <input
            type="search"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Search by name or email"
            className="w-full rounded-md border bg-background py-1.5 pl-8 pr-3 text-sm outline-none focus:ring-2 focus:ring-primary/30"
          />
        </div>
      </div>

      {/* Table */}
      <div className="flex-1 overflow-auto">
        {isLoading ? (
          <div className="flex items-center justify-center py-16 text-sm text-muted-foreground">
            Loading...
          </div>
        ) : users.length === 0 ? (
          <div className="flex flex-col items-center justify-center py-16 text-muted-foreground">
            <Contact className="mb-3 h-8 w-8 opacity-30" />
            <p className="text-sm">{query ? "No matching users" : "No users yet"}</p>
            {!isReadOnly && !query && (
              <p className="mt-1 text-xs opacity-60">
                People appear here as they sign in, or add one above.
              </p>
            )}
          </div>
        ) : (
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b bg-muted/30 text-left text-xs font-medium text-muted-foreground">
                <th className="px-5 py-2">Name</th>
                <th className="px-5 py-2">Email</th>
                <th className="px-5 py-2">Status</th>
                <th className="px-5 py-2">Last Seen</th>
                {!isReadOnly && <th className="w-28 px-5 py-2">Actions</th>}
              </tr>
            </thead>
            <tbody className="divide-y">
              {users.map((u) => (
                <UserRow
                  key={u.email}
                  user={u}
                  isReadOnly={isReadOnly}
                  deleteConfirm={deleteConfirm === u.email}
                  deleting={deleteMutation.isPending}
                  onRequestDelete={() => setDeleteConfirm(u.email)}
                  onCancelDelete={() => setDeleteConfirm(null)}
                  onConfirmDelete={() => handleDelete(u.email)}
                />
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// User row — view + inline edit
// ---------------------------------------------------------------------------

interface UserRowProps {
  user: DirectoryUser;
  isReadOnly: boolean;
  deleteConfirm: boolean;
  deleting: boolean;
  onRequestDelete: () => void;
  onCancelDelete: () => void;
  onConfirmDelete: () => void;
}

function UserRow({
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
      <tr className="bg-muted/20">
        <td className="px-5 py-2" colSpan={2}>
          <div className="flex gap-2">
            <input
              value={first}
              onChange={(e) => setFirst(e.target.value)}
              placeholder="First name"
              className="w-32 rounded-md border bg-background px-2 py-1 text-sm outline-none focus:ring-2 focus:ring-primary/30"
            />
            <input
              value={last}
              onChange={(e) => setLast(e.target.value)}
              placeholder="Last name"
              className="w-32 rounded-md border bg-background px-2 py-1 text-sm outline-none focus:ring-2 focus:ring-primary/30"
            />
            <span className="self-center text-xs text-muted-foreground">{user.email}</span>
          </div>
        </td>
        <td className="px-5 py-2" colSpan={3}>
          <div className="flex items-center gap-1.5">
            <button
              type="button"
              onClick={save}
              disabled={updateMutation.isPending}
              className="inline-flex items-center gap-1 rounded bg-primary px-2 py-1 text-xs font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
            >
              <Check className="h-3 w-3" />
              {updateMutation.isPending ? "Saving..." : "Save"}
            </button>
            <button
              type="button"
              onClick={() => {
                setFirst(user.first_name);
                setLast(user.last_name);
                setEditing(false);
              }}
              className="inline-flex items-center rounded border px-1.5 py-1 text-xs text-muted-foreground hover:bg-muted"
            >
              <X className="h-3 w-3" />
            </button>
          </div>
        </td>
      </tr>
    );
  }

  return (
    <tr className="transition-colors hover:bg-muted/20">
      <td className="px-5 py-3 font-medium">{fullName(user)}</td>
      <td className="px-5 py-3 text-muted-foreground">{user.email}</td>
      <td className="px-5 py-3">
        <StatusBadge user={user} />
      </td>
      <td className="px-5 py-3 text-muted-foreground">{formatSeen(user.last_seen_at)}</td>
      {!isReadOnly && (
        <td className="px-5 py-3">
          {deleteConfirm ? (
            <div className="flex items-center gap-1.5">
              <button
                type="button"
                onClick={onConfirmDelete}
                disabled={deleting}
                className="inline-flex items-center gap-1 rounded bg-red-600 px-2 py-1 text-xs font-medium text-white hover:bg-red-700 disabled:opacity-50"
              >
                {deleting ? "..." : "Confirm"}
              </button>
              <button
                type="button"
                onClick={onCancelDelete}
                className="inline-flex items-center rounded border px-1.5 py-1 text-xs text-muted-foreground hover:bg-muted"
              >
                <X className="h-3 w-3" />
              </button>
            </div>
          ) : (
            <div className="flex items-center gap-1">
              <button
                type="button"
                onClick={() => {
                  // Seed the inputs from the current props at edit time, not at
                  // mount, so a row whose data was refetched while displayed
                  // does not prefill stale names.
                  setFirst(user.first_name);
                  setLast(user.last_name);
                  setEditing(true);
                }}
                className="inline-flex items-center gap-1 rounded border border-transparent px-2 py-1 text-xs text-muted-foreground hover:border-border hover:text-foreground"
              >
                <Pencil className="h-3 w-3" />
                Edit
              </button>
              <button
                type="button"
                onClick={onRequestDelete}
                className="inline-flex items-center rounded border border-transparent px-1.5 py-1 text-xs text-muted-foreground hover:border-red-200 hover:text-red-600 dark:hover:border-red-800 dark:hover:text-red-400"
              >
                <Trash2 className="h-3 w-3" />
              </button>
            </div>
          )}
        </td>
      )}
    </tr>
  );
}

function StatusBadge({ user }: { user: DirectoryUser }) {
  if (user.confirmed) {
    return (
      <span className="rounded-full bg-green-100 px-2 py-0.5 text-xs font-medium text-green-700 dark:bg-green-900/30 dark:text-green-400">
        Active
      </span>
    );
  }
  return (
    <span
      className="rounded-full bg-amber-100 px-2 py-0.5 text-xs font-medium text-amber-700 dark:bg-amber-900/30 dark:text-amber-400"
      title={user.added_by ? `Added by ${user.added_by}` : undefined}
    >
      Invited
    </span>
  );
}

// ---------------------------------------------------------------------------
// Add user form (inline above the table)
// ---------------------------------------------------------------------------

function AddUserForm({ onDone }: { onDone: () => void }) {
  const createMutation = useCreateUser();
  const [email, setEmail] = useState("");
  const [first, setFirst] = useState("");
  const [last, setLast] = useState("");
  const [error, setError] = useState<string | null>(null);

  const handleSubmit = useCallback(() => {
    if (!email.trim()) {
      setError("Email is required");
      return;
    }
    createMutation.mutate(
      {
        email: email.trim(),
        first_name: first.trim() || undefined,
        last_name: last.trim() || undefined,
      },
      {
        onSuccess: () => {
          setEmail("");
          setFirst("");
          setLast("");
          onDone();
        },
        onError: (err) => setError(err instanceof Error ? err.message : "Failed to add user"),
      },
    );
  }, [email, first, last, createMutation, onDone]);

  return (
    <div className="space-y-3 border-b bg-muted/10 px-5 py-4">
      {error && (
        <div className="rounded-md border border-red-200 bg-red-50 px-4 py-2.5 text-xs text-red-700 dark:border-red-800 dark:bg-red-950/30 dark:text-red-400">
          {error}
        </div>
      )}
      <div className="grid grid-cols-3 gap-3">
        <div>
          <label className="mb-1 block text-xs font-medium">
            Email <span className="text-red-500">*</span>
          </label>
          <input
            type="email"
            value={email}
            onChange={(e) => {
              setEmail(e.target.value);
              setError(null);
            }}
            placeholder="e.g. marcus.johnson@example.com"
            className="w-full rounded-md border bg-background px-3 py-1.5 text-sm outline-none focus:ring-2 focus:ring-primary/30"
          />
        </div>
        <div>
          <label className="mb-1 block text-xs font-medium">First name</label>
          <input
            type="text"
            value={first}
            onChange={(e) => setFirst(e.target.value)}
            placeholder="Marcus"
            className="w-full rounded-md border bg-background px-3 py-1.5 text-sm outline-none focus:ring-2 focus:ring-primary/30"
          />
        </div>
        <div>
          <label className="mb-1 block text-xs font-medium">Last name</label>
          <input
            type="text"
            value={last}
            onChange={(e) => setLast(e.target.value)}
            placeholder="Johnson"
            className="w-full rounded-md border bg-background px-3 py-1.5 text-sm outline-none focus:ring-2 focus:ring-primary/30"
          />
        </div>
      </div>
      <div className="flex justify-end">
        <button
          type="button"
          onClick={handleSubmit}
          disabled={createMutation.isPending || !email.trim()}
          className="inline-flex items-center gap-1.5 rounded-md bg-primary px-4 py-1.5 text-xs font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
        >
          <Plus className="h-3.5 w-3.5" />
          {createMutation.isPending ? "Adding..." : "Add User"}
        </button>
      </div>
    </div>
  );
}
