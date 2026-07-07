import { useEffect, useState } from "react";
import { AlertCircle, Check, Copy, Trash2 } from "lucide-react";

import {
  useAPICatalog,
  useCloneAPICatalog,
  useDeleteAPICatalog,
  useUpdateAPICatalog,
} from "@/api/admin/hooks";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { PromptDialog } from "@/components/PromptDialog";
import { LabeledInput, LabeledTextarea } from "./forms";
import { SpecsManager } from "./SpecsManager";

// ---------------------------------------------------------------------------
// Catalog editor (header + SpecsManager)
// ---------------------------------------------------------------------------

export function CatalogEditor({
  catalogID,
  isReadOnly,
  onDeleted,
}: {
  catalogID: string;
  isReadOnly: boolean;
  onDeleted: () => void;
}) {
  const { data: catalog, isLoading } = useAPICatalog(catalogID);
  const update = useUpdateAPICatalog();
  const del = useDeleteAPICatalog();
  const clone = useCloneAPICatalog();

  const [editing, setEditing] = useState(false);
  const [draftName, setDraftName] = useState("");
  const [draftVersion, setDraftVersion] = useState("");
  const [draftDisplayName, setDraftDisplayName] = useState("");
  const [draftDescription, setDraftDescription] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [confirmDeleteOpen, setConfirmDeleteOpen] = useState(false);
  const [deleteError, setDeleteError] = useState<string | null>(null);
  const [cloneOpen, setCloneOpen] = useState(false);
  const [cloneError, setCloneError] = useState<string | null>(null);

  useEffect(() => {
    if (catalog) {
      setDraftName(catalog.name);
      setDraftVersion(catalog.version ?? "");
      setDraftDisplayName(catalog.display_name);
      setDraftDescription(catalog.description ?? "");
    }
  }, [catalog]);

  const handleSave = async () => {
    setError(null);
    try {
      await update.mutateAsync({
        id: catalogID,
        name: draftName,
        version: draftVersion,
        display_name: draftDisplayName,
        description: draftDescription,
      });
      setEditing(false);
    } catch (e) {
      setError(e instanceof Error ? e.message : "save failed");
    }
  };

  const handleDeleteConfirmed = async () => {
    setDeleteError(null);
    try {
      await del.mutateAsync(catalogID);
      setConfirmDeleteOpen(false);
      onDeleted();
    } catch (e) {
      setDeleteError(e instanceof Error ? e.message : "delete failed");
    }
  };

  const handleCloneConfirmed = async (values: Record<string, string>) => {
    const newID = values.id?.trim();
    if (!newID) return;
    setCloneError(null);
    try {
      await clone.mutateAsync({
        sourceID: catalogID,
        id: newID,
        name: catalog?.name,
        version: values.version?.trim() || undefined,
      });
      setCloneOpen(false);
    } catch (e) {
      setCloneError(e instanceof Error ? e.message : "clone failed");
    }
  };

  if (isLoading || !catalog) {
    return <div className="text-sm text-muted-foreground">Loading…</div>;
  }

  return (
    <div className="space-y-6">
      <div className="space-y-3">
        {!isReadOnly && (
          <div className="flex flex-wrap justify-end gap-2">
            {editing ? (
              <>
                <button
                  type="button"
                  onClick={handleSave}
                  disabled={update.isPending}
                  className="inline-flex items-center gap-1 rounded-md bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground hover:opacity-90 disabled:opacity-50"
                >
                  <Check className="h-4 w-4" /> Save
                </button>
                <button
                  type="button"
                  onClick={() => setEditing(false)}
                  className="rounded-md border bg-background px-3 py-1.5 text-sm hover:bg-muted"
                >
                  Cancel
                </button>
              </>
            ) : (
              <>
                <button
                  type="button"
                  onClick={() => setEditing(true)}
                  className="rounded-md border bg-background px-3 py-1.5 text-sm hover:bg-muted"
                >
                  Edit
                </button>
                <button
                  type="button"
                  onClick={() => setCloneOpen(true)}
                  className="inline-flex items-center gap-1 rounded-md border bg-background px-3 py-1.5 text-sm hover:bg-muted"
                >
                  <Copy className="h-4 w-4" /> Clone
                </button>
                <button
                  type="button"
                  onClick={() => setConfirmDeleteOpen(true)}
                  disabled={catalog.ref_count > 0}
                  title={catalog.ref_count > 0 ? "Cannot delete; still referenced by a connection" : ""}
                  className="inline-flex items-center gap-1 rounded-md border bg-background px-3 py-1.5 text-sm text-destructive hover:bg-destructive/10 disabled:opacity-50"
                >
                  <Trash2 className="h-4 w-4" /> Delete
                </button>
              </>
            )}
          </div>
        )}

        {editing ? (
          <div className="grid grid-cols-1 gap-3 md:grid-cols-2 md:max-w-2xl">
            <div className="md:col-span-2">
              <LabeledInput
                label="Catalog name"
                help="Human-readable name shown to operators."
                value={draftDisplayName}
                onChange={setDraftDisplayName}
              />
            </div>
            <LabeledInput
              label="Internal slug"
              help="Machine-readable family slug shared across versions."
              value={draftName}
              onChange={setDraftName}
              mono
            />
            <LabeledInput
              label="Version"
              help="Free-text version label."
              value={draftVersion}
              onChange={setDraftVersion}
              mono
            />
            <div className="md:col-span-2">
              <LabeledTextarea
                label="Description"
                value={draftDescription}
                onChange={setDraftDescription}
              />
            </div>
          </div>
        ) : (
          <div>
            <h2 className="text-lg font-semibold break-words">{catalog.display_name}</h2>
            <div className="mt-0.5 flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
              <code className="break-all">{catalog.id}</code>
              {catalog.version && (
                <span className="rounded bg-muted px-1.5 py-0.5">v{catalog.version}</span>
              )}
              {catalog.ref_count > 0 && (
                <span>· referenced by {catalog.ref_count} connection{catalog.ref_count === 1 ? "" : "s"}</span>
              )}
            </div>
            {catalog.description && (
              <p className="mt-2 text-sm text-muted-foreground">{catalog.description}</p>
            )}
          </div>
        )}
      </div>

      {error && (
        <div className="flex items-start gap-2 rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive">
          <AlertCircle className="mt-0.5 h-4 w-4 shrink-0" />
          <span>{error}</span>
        </div>
      )}

      <SpecsManager catalogID={catalogID} isReadOnly={isReadOnly} />

      <ConfirmDialog
        open={confirmDeleteOpen}
        onOpenChange={(open) => {
          setConfirmDeleteOpen(open);
          if (!open) setDeleteError(null);
        }}
        destructive
        title="Delete catalog?"
        description={
          <>
            The catalog <code className="font-mono">{catalog.id}</code> and all
            of its component specs will be removed. This cannot be undone.
          </>
        }
        confirmLabel="Delete"
        loading={del.isPending}
        error={deleteError}
        onConfirm={handleDeleteConfirmed}
      />

      <PromptDialog
        open={cloneOpen}
        onOpenChange={(open) => {
          setCloneOpen(open);
          if (!open) setCloneError(null);
        }}
        title="Clone catalog"
        description={
          <>
            Clones the catalog header and every component spec into a new
            row. Pick a new ID (immutable) and an optional new version.
          </>
        }
        fields={[
          {
            name: "id",
            label: "New catalog ID",
            placeholder: "salesforce-rest-2025-01",
            required: true,
            monospace: true,
            help: "Lowercase, hyphens, no spaces. Immutable after creation.",
          },
          {
            name: "version",
            label: "Version (optional)",
            placeholder: "2025-01",
            monospace: true,
            help: "Free-text label. Leave blank to clone without a version label.",
          },
        ]}
        confirmLabel="Clone"
        loading={clone.isPending}
        error={cloneError}
        onConfirm={handleCloneConfirmed}
      />
    </div>
  );
}
