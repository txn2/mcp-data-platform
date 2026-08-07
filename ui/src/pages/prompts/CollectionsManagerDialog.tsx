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
import { EmptyState } from "@/components/patterns/EmptyState";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { ModalScroll } from "@/components/ModalShell";
import { FormError, ListSkeleton } from "./primitives";

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
  const canManage = (c: PromptCollection) =>
    isAdmin || c.created_by === myEmail;

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
      updateMutation.mutate(
        { id: editingId, ...body },
        { onSuccess: resetForm, onError: reportError },
      );
    } else {
      createMutation.mutate(body, {
        onSuccess: resetForm,
        onError: reportError,
      });
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
    <ModalScroll onClose={onClose} width="max-w-lg" label="Manage collections">
      <div className="rounded-lg border bg-background shadow-lg">
        <div className="flex items-center justify-between border-b px-4 py-3">
          <h3 className="flex items-center gap-2 text-sm font-semibold">
            <FolderOpen className="size-4 text-muted-foreground" /> Manage
            collections
          </h3>
          <Button variant="ghost" size="icon-sm" onClick={onClose} aria-label="Close">
            <X />
          </Button>
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
    </ModalScroll>
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
      <Input
        value={name}
        onChange={(e) => setName(e.target.value)}
        placeholder="Collection name"
        aria-label="Collection name"
      />
      <Input
        value={description}
        onChange={(e) => setDescription(e.target.value)}
        placeholder="Description (optional)"
        aria-label="Collection description"
      />
      <FormError message={error} />
      <div className="flex justify-end gap-2">
        {editing && (
          <Button variant="outline" onClick={onCancel}>
            Cancel
          </Button>
        )}
        <Button onClick={onSubmit} disabled={!name.trim() || pending}>
          <Plus />
          {pending ? "Saving..." : editing ? "Save" : "Create"}
        </Button>
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
    return (
      <div className="px-4 py-4">
        <ListSkeleton rows={3} />
      </div>
    );
  }
  if (collections.length === 0) {
    return (
      <div className="px-4 py-4">
        <EmptyState icon={FolderOpen}>
          No collections yet. Create one below to group prompts by team, domain, or workflow.
        </EmptyState>
      </div>
    );
  }
  return (
    <ul className="max-h-72 divide-y overflow-y-auto">
      {collections.map((c) => (
        <li key={c.id} className="flex items-center gap-2 px-4 py-2.5 text-sm">
          <div className="min-w-0 flex-1">
            <div className="truncate font-medium">{c.name}</div>
            {c.description && (
              <div className="truncate text-xs text-muted-foreground">
                {markdownToPlainText(c.description)}
              </div>
            )}
          </div>
          <span className="text-xs whitespace-nowrap text-muted-foreground">
            {c.prompt_count} prompt{c.prompt_count === 1 ? "" : "s"}
          </span>
          {canManage(c) && (
            <>
              <Button
                variant="ghost"
                size="icon-sm"
                onClick={() => onEdit(c)}
                aria-label={`Rename ${c.name}`}
              >
                <Pencil />
              </Button>
              <Button
                variant="ghost"
                size="icon-sm"
                onClick={() => onDelete(c)}
                disabled={deletePending}
                aria-label={`Delete ${c.name}`}
                title="Delete collection (its prompts stay, ungrouped)"
                className="text-destructive hover:bg-destructive/10 hover:text-destructive"
              >
                <Trash2 />
              </Button>
            </>
          )}
        </li>
      ))}
    </ul>
  );
}
