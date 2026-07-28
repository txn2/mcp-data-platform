import { useState } from "react";
import { FolderOpen, Pencil, Plus, Trash2, X } from "lucide-react";
import { markdownToPlainText } from "@/lib/markdownText";
import { useAuthStore } from "@/stores/auth";
import {
  usePromptCollections,
  useCreatePromptCollection,
  useUpdatePromptCollection,
  useDeletePromptCollection,
} from "@/api/portal/hooks";
import type { PromptCollection } from "@/api/admin/types";

// CollectionsManagerDialog creates, renames, and deletes prompt collections
// (#1010). Any user can create a collection; renaming and deleting are limited
// to the collection's creator or an admin (mirrored server-side). Deleting a
// collection releases its prompts to the default group, so it is safe.
export function CollectionsManagerDialog({ onClose }: { onClose: () => void }) {
  const { data, isLoading } = usePromptCollections();
  const createMutation = useCreatePromptCollection();
  const updateMutation = useUpdatePromptCollection();
  const deleteMutation = useDeletePromptCollection();
  const myEmail = useAuthStore((s) => s.user?.email) ?? "";
  const isAdmin = useAuthStore((s) => s.isAdmin)();

  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [editingId, setEditingId] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const collections = data?.data ?? [];
  const canManage = (c: PromptCollection) => isAdmin || c.created_by === myEmail;

  function reportError(err: unknown) {
    setError(err instanceof Error ? err.message : "Operation failed");
  }

  function resetForm() {
    setName("");
    setDescription("");
    setEditingId(null);
    setError(null);
  }

  function handleSubmit() {
    setError(null);
    const body = { name, description };
    if (editingId) {
      updateMutation.mutate({ id: editingId, ...body }, { onSuccess: resetForm, onError: reportError });
    } else {
      createMutation.mutate(body, { onSuccess: resetForm, onError: reportError });
    }
  }

  function startEdit(c: PromptCollection) {
    setEditingId(c.id);
    setName(c.name);
    setDescription(c.description);
    setError(null);
  }

  function handleDelete(c: PromptCollection) {
    setError(null);
    // Deleting the collection being renamed would leave a stale edit form
    // pointing at a dead id.
    if (editingId === c.id) resetForm();
    deleteMutation.mutate(c.id, { onError: reportError });
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4" role="dialog" aria-label="Manage collections">
      <div className="w-full max-w-lg rounded-lg border bg-background shadow-lg">
        <div className="flex items-center justify-between border-b px-4 py-3">
          <h3 className="flex items-center gap-2 text-sm font-semibold">
            <FolderOpen className="h-4 w-4 text-muted-foreground" /> Manage collections
          </h3>
          <button onClick={onClose} className="rounded-md p-1 hover:bg-accent" aria-label="Close">
            <X className="h-4 w-4" />
          </button>
        </div>

        <CollectionList
          collections={collections}
          isLoading={isLoading}
          canManage={canManage}
          deletePending={deleteMutation.isPending}
          onEdit={startEdit}
          onDelete={handleDelete}
        />

        <CollectionEditorForm
          editing={editingId !== null}
          name={name}
          description={description}
          error={error}
          pending={createMutation.isPending || updateMutation.isPending}
          setName={setName}
          setDescription={setDescription}
          onCancel={resetForm}
          onSubmit={handleSubmit}
        />
      </div>
    </div>
  );
}

function CollectionEditorForm({
  editing,
  name,
  description,
  error,
  pending,
  setName,
  setDescription,
  onCancel,
  onSubmit,
}: {
  editing: boolean;
  name: string;
  description: string;
  error: string | null;
  pending: boolean;
  setName: (v: string) => void;
  setDescription: (v: string) => void;
  onCancel: () => void;
  onSubmit: () => void;
}) {
  return (
    <div className="space-y-2 border-t px-4 py-3">
      <div className="text-xs font-medium text-muted-foreground">
        {editing ? "Rename collection" : "New collection"}
      </div>
      <input
        value={name}
        onChange={(e) => setName(e.target.value)}
        placeholder="Collection name"
        className="w-full rounded-md border bg-background px-3 py-1.5 text-sm outline-none ring-ring focus:ring-2"
      />
      <input
        value={description}
        onChange={(e) => setDescription(e.target.value)}
        placeholder="Description (optional)"
        className="w-full rounded-md border bg-background px-3 py-1.5 text-sm outline-none ring-ring focus:ring-2"
      />
      {error && (
        <div className="rounded-md border border-red-500/20 bg-red-500/10 px-3 py-2 text-xs text-red-400">{error}</div>
      )}
      <div className="flex justify-end gap-2">
        {editing && (
          <button onClick={onCancel} className="rounded-md border px-3 py-1.5 text-sm hover:bg-muted">
            Cancel
          </button>
        )}
        <button
          onClick={onSubmit}
          disabled={!name.trim() || pending}
          className="inline-flex items-center gap-1.5 rounded-md bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
        >
          <Plus className="h-3.5 w-3.5" />
          {pending ? "Saving..." : editing ? "Save" : "Create"}
        </button>
      </div>
    </div>
  );
}

function CollectionList({
  collections,
  isLoading,
  canManage,
  deletePending,
  onEdit,
  onDelete,
}: {
  collections: PromptCollection[];
  isLoading: boolean;
  canManage: (c: PromptCollection) => boolean;
  deletePending: boolean;
  onEdit: (c: PromptCollection) => void;
  onDelete: (c: PromptCollection) => void;
}) {
  if (isLoading) {
    return <div className="px-4 py-6 text-center text-sm text-muted-foreground">Loading...</div>;
  }
  if (collections.length === 0) {
    return (
      <div className="px-4 py-6 text-center text-sm text-muted-foreground">
        No collections yet. Create one below to group prompts by team, domain, or workflow.
      </div>
    );
  }
  return (
    <ul className="max-h-72 divide-y overflow-y-auto">
      {collections.map((c) => (
        <li key={c.id} className="flex items-center gap-2 px-4 py-2.5 text-sm">
          <div className="min-w-0 flex-1">
            <div className="font-medium truncate">{c.name}</div>
            {c.description && (
              <div className="text-xs text-muted-foreground truncate">{markdownToPlainText(c.description)}</div>
            )}
          </div>
          <span className="text-xs text-muted-foreground whitespace-nowrap">
            {c.prompt_count} prompt{c.prompt_count === 1 ? "" : "s"}
          </span>
          {canManage(c) && (
            <>
              <button onClick={() => onEdit(c)} className="rounded-md p-1.5 hover:bg-accent" aria-label={`Rename ${c.name}`}>
                <Pencil className="h-3.5 w-3.5" />
              </button>
              <button
                onClick={() => onDelete(c)}
                disabled={deletePending}
                className="rounded-md p-1.5 text-destructive hover:bg-destructive/10 disabled:opacity-50"
                aria-label={`Delete ${c.name}`}
                title="Delete collection (its prompts stay, ungrouped)"
              >
                <Trash2 className="h-3.5 w-3.5" />
              </button>
            </>
          )}
        </li>
      ))}
    </ul>
  );
}
