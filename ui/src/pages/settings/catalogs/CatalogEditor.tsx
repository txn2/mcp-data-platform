import { useEffect, useState } from "react";
import { AlertCircle, Check, Copy, Trash2 } from "lucide-react";

import {
  useAPICatalog,
  useCloneAPICatalog,
  useDeleteAPICatalog,
  useUpdateAPICatalog,
} from "@/api/admin/hooks";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { CollapsibleMarkdown } from "@/components/renderers/CollapsibleMarkdown";
import { PromptDialog } from "@/components/PromptDialog";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
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
                <Button type="button" size="sm" onClick={handleSave} disabled={update.isPending}>
                  <Check /> Save
                </Button>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={() => setEditing(false)}
                >
                  Cancel
                </Button>
              </>
            ) : (
              <>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={() => setEditing(true)}
                >
                  Edit
                </Button>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={() => setCloneOpen(true)}
                >
                  <Copy /> Clone
                </Button>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={() => setConfirmDeleteOpen(true)}
                  disabled={catalog.ref_count > 0}
                  title={catalog.ref_count > 0 ? "Cannot delete; still referenced by a connection" : ""}
                  className="text-destructive hover:bg-destructive/10 hover:text-destructive"
                >
                  <Trash2 /> Delete
                </Button>
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
              {catalog.version && <Badge variant="muted">v{catalog.version}</Badge>}
              {catalog.ref_count > 0 && (
                <span>· referenced by {catalog.ref_count} connection{catalog.ref_count === 1 ? "" : "s"}</span>
              )}
            </div>
            {catalog.description && (
              <div className="mt-2 text-sm text-muted-foreground">
                <CollapsibleMarkdown content={catalog.description} maxHeightPx={160} />
              </div>
            )}
          </div>
        )}
      </div>

      {error && (
        <Alert variant="destructive">
          <AlertCircle />
          <AlertDescription>{error}</AlertDescription>
        </Alert>
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
