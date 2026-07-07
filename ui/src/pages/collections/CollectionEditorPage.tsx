import { useState, useEffect, useCallback } from "react";
import { ArrowLeft, Plus, Trash2, Save } from "lucide-react";
import {
  DndContext,
  closestCenter,
  KeyboardSensor,
  PointerSensor,
  useSensor,
  useSensors,
  type DragEndEvent,
} from "@dnd-kit/core";
import {
  arrayMove,
  SortableContext,
  sortableKeyboardCoordinates,
  verticalListSortingStrategy,
} from "@dnd-kit/sortable";
import {
  useCollection,
  useUpdateCollection,
  useUpdateCollectionSections,
  useUpdateCollectionConfig,
  useDeleteCollection,
  useAssets,
} from "@/api/portal/hooks";
import type { Asset, CollectionConfig } from "@/api/portal/types";
import { MarkdownEditor } from "@/components/MarkdownEditor";
import { SortableSection } from "./editor/SortableSection";
import { type SectionDraft, draftId } from "./editor/types";

interface Props {
  collectionId: string;
  onBack: () => void;
  onNavigate: (path: string) => void;
}

export function CollectionEditorPage({ collectionId, onBack, onNavigate }: Props) {
  const { data: coll, isLoading } = useCollection(collectionId);
  const updateMutation = useUpdateCollection();
  const sectionsMutation = useUpdateCollectionSections();
  const configMutation = useUpdateCollectionConfig();
  const deleteMutation = useDeleteCollection();
  const { data: assetsData } = useAssets({ limit: 200 });

  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [config, setConfig] = useState<CollectionConfig>({});
  const [sections, setSections] = useState<SectionDraft[]>([]);
  const [initialized, setInitialized] = useState(false);
  const [deleteConfirm, setDeleteConfirm] = useState(false);

  const sectionSensors = useSensors(
    useSensor(PointerSensor),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }),
  );

  useEffect(() => {
    if (coll && !initialized) {
      setName(coll.name);
      setDescription(coll.description);
      setConfig(coll.config || {});
      setSections(
        (coll.sections || []).map((s) => ({
          id: s.id || draftId(),
          title: s.title,
          description: s.description,
          items: (s.items || []).map((item) => ({
            id: item.id || draftId(),
            asset_id: item.asset_id,
            assetName: item.asset_name,
            assetContentType: item.asset_content_type,
          })),
        })),
      );
      setInitialized(true);
    }
  }, [coll, initialized]);

  const assets: Asset[] = assetsData?.data ?? [];

  const addSection = useCallback(() => {
    setSections((prev) => [
      ...prev,
      { id: draftId(), title: "", description: "", items: [] },
    ]);
  }, []);

  const removeSection = useCallback((index: number) => {
    setSections((prev) => prev.filter((_, i) => i !== index));
  }, []);

  const updateSection = useCallback((index: number, field: "title" | "description", value: string) => {
    setSections((prev) =>
      prev.map((s, i) => (i === index ? { ...s, [field]: value } : s)),
    );
  }, []);

  const addItem = useCallback((sectionIndex: number, assetId: string, assetName: string, assetContentType: string) => {
    setSections((prev) =>
      prev.map((s, i) =>
        i === sectionIndex
          ? { ...s, items: [...s.items, { id: draftId(), asset_id: assetId, assetName, assetContentType }] }
          : s,
      ),
    );
  }, []);

  const removeItem = useCallback((sectionIndex: number, itemIndex: number) => {
    setSections((prev) =>
      prev.map((s, i) =>
        i === sectionIndex
          ? { ...s, items: s.items.filter((_, j) => j !== itemIndex) }
          : s,
      ),
    );
  }, []);

  const reorderItems = useCallback((sectionIndex: number, oldIndex: number, newIndex: number) => {
    setSections((prev) =>
      prev.map((s, i) =>
        i === sectionIndex
          ? { ...s, items: arrayMove(s.items, oldIndex, newIndex) }
          : s,
      ),
    );
  }, []);

  function handleSectionDragEnd(event: DragEndEvent) {
    const { active, over } = event;
    if (!over || active.id === over.id) return;

    setSections((prev) => {
      const oldIndex = prev.findIndex((s) => s.id === active.id);
      const newIndex = prev.findIndex((s) => s.id === over.id);
      if (oldIndex === -1 || newIndex === -1) return prev;
      return arrayMove(prev, oldIndex, newIndex);
    });
  }

  async function handleSave() {
    await updateMutation.mutateAsync({ id: collectionId, name, description });
    await configMutation.mutateAsync({ id: collectionId, config });

    await sectionsMutation.mutateAsync({
      id: collectionId,
      sections: sections.map((s) => ({
        title: s.title,
        description: s.description,
        items: s.items.map((item) => ({ asset_id: item.asset_id })),
      })),
    });

    onBack();
  }

  const isSaving = updateMutation.isPending || sectionsMutation.isPending || configMutation.isPending;

  if (isLoading) {
    return (
      <div className="flex items-center justify-center py-12 text-muted-foreground">
        Loading...
      </div>
    );
  }

  return (
    <div className="space-y-6 max-w-3xl">
      {/* Toolbar */}
      <div className="flex items-center gap-3">
        <button
          onClick={onBack}
          className="flex items-center gap-1.5 text-sm text-muted-foreground hover:text-foreground"
        >
          <ArrowLeft className="h-4 w-4" />
          Back
        </button>
        <div className="flex-1" />
        {deleteConfirm ? (
          <div className="flex items-center gap-2">
            <span className="text-xs text-destructive">Delete this collection?</span>
            <button
              onClick={async () => {
                await deleteMutation.mutateAsync(collectionId);
                onNavigate("/collections");
              }}
              disabled={deleteMutation.isPending}
              className="rounded-md bg-destructive px-3 py-1.5 text-xs font-medium text-destructive-foreground hover:bg-destructive/90 disabled:opacity-50"
            >
              {deleteMutation.isPending ? "Deleting..." : "Yes, Delete"}
            </button>
            <button
              onClick={() => setDeleteConfirm(false)}
              className="rounded-md bg-secondary px-3 py-1.5 text-xs font-medium text-secondary-foreground hover:bg-secondary/80"
            >
              Cancel
            </button>
          </div>
        ) : (
          <button
            onClick={() => setDeleteConfirm(true)}
            className="flex items-center gap-1.5 text-sm text-muted-foreground hover:text-destructive"
            title="Delete collection"
          >
            <Trash2 className="h-3.5 w-3.5" />
          </button>
        )}
        <button
          onClick={() => void handleSave()}
          disabled={isSaving || !name}
          className="flex items-center gap-1.5 rounded-md bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
        >
          <Save className="h-3.5 w-3.5" />
          {isSaving ? "Saving..." : "Save"}
        </button>
      </div>

      {/* Name */}
      <div>
        <label className="block text-sm font-medium mb-1">Name</label>
        <input
          type="text"
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="Collection name"
          className="w-full rounded-md border bg-background px-3 py-2 text-sm outline-none ring-ring focus:ring-2"
        />
      </div>

      {/* Description with markdown editor */}
      <div>
        <label className="block text-sm font-medium mb-1">Description</label>
        <MarkdownEditor
          value={description}
          onChange={setDescription}
          placeholder="Describe this collection (supports markdown)..."
          minHeight="200px"
        />
      </div>

      {/* Settings */}
      <div>
        <label className="block text-sm font-medium mb-2">Settings</label>
        <div className="rounded-lg border bg-card p-4 space-y-3">
          <div className="flex items-center justify-between">
            <div>
              <span className="text-sm font-medium">Thumbnail Size</span>
              <p className="text-xs text-muted-foreground">Controls how asset thumbnails display in the collection viewer</p>
            </div>
            <select
              value={config.thumbnail_size || "large"}
              onChange={(e) => setConfig({ ...config, thumbnail_size: e.target.value as CollectionConfig["thumbnail_size"] })}
              className="rounded-md border bg-background px-3 py-1.5 text-sm"
            >
              <option value="large">Large</option>
              <option value="medium">Medium</option>
              <option value="small">Small</option>
              <option value="none">No thumbnails</option>
            </select>
          </div>
        </div>
      </div>

      {/* Sections */}
      <div>
        <div className="flex items-center justify-between mb-3">
          <label className="text-sm font-medium">Sections</label>
          <button
            onClick={addSection}
            className="flex items-center gap-1 text-xs text-primary hover:underline"
          >
            <Plus className="h-3 w-3" />
            Add Section
          </button>
        </div>

        <DndContext
          sensors={sectionSensors}
          collisionDetection={closestCenter}
          onDragEnd={handleSectionDragEnd}
        >
          <SortableContext items={sections.map((s) => s.id)} strategy={verticalListSortingStrategy}>
            <div className="space-y-3">
              {sections.map((section, index) => (
                <SortableSection
                  key={section.id}
                  section={section}
                  index={index}
                  onUpdate={updateSection}
                  onRemove={removeSection}
                  onAddItem={addItem}
                  onRemoveItem={removeItem}
                  onReorderItems={reorderItems}
                  assets={assets}
                />
              ))}
            </div>
          </SortableContext>
        </DndContext>

        {sections.length === 0 && (
          <div className="text-center py-8 text-muted-foreground text-sm border rounded-lg border-dashed">
            No sections yet. Click "Add Section" to get started.
          </div>
        )}
      </div>
    </div>
  );
}
