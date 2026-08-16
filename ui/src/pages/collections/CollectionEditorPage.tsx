import { useState, useEffect, useCallback } from "react";
import { Plus } from "lucide-react";
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
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { MarkdownEditor } from "@/components/MarkdownEditor";
import { EmptyState } from "@/components/patterns/EmptyState";
import { PageHeader } from "@/components/patterns/PageHeader";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { EditorHeaderActions } from "./editor/EditorHeaderActions";
import { SortableSection } from "./editor/SortableSection";
import { ViewOnlyNotice } from "./editor/ViewOnlyNotice";
import { type SectionDraft, draftId } from "./editor/types";

interface Props {
  collectionId: string;
  onBack: () => void;
  onNavigate: (path: string) => void;
}

const THUMBNAIL_SIZES = [
  { value: "large", label: "Large" },
  { value: "medium", label: "Medium" },
  { value: "small", label: "Small" },
  { value: "none", label: "No thumbnails" },
];

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
  const [deleteOpen, setDeleteOpen] = useState(false);

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

  const assets: Asset[] = assetList(assetsData);

  const addSection = useCallback(() => {
    setSections((prev) => [...prev, { id: draftId(), title: "", description: "", items: [] }]);
  }, []);

  const removeSection = useCallback((index: number) => {
    setSections((prev) => prev.filter((_, i) => i !== index));
  }, []);

  const updateSection = useCallback(
    (index: number, field: "title" | "description", value: string) => {
      setSections((prev) => prev.map((s, i) => (i === index ? { ...s, [field]: value } : s)));
    },
    [],
  );

  const addItem = useCallback(
    (sectionIndex: number, assetId: string, assetName: string, assetContentType: string) => {
      setSections((prev) =>
        prev.map((s, i) =>
          i === sectionIndex
            ? {
                ...s,
                items: [...s.items, { id: draftId(), asset_id: assetId, assetName, assetContentType }],
              }
            : s,
        ),
      );
    },
    [],
  );

  const removeItem = useCallback((sectionIndex: number, itemIndex: number) => {
    setSections((prev) =>
      prev.map((s, i) =>
        i === sectionIndex ? { ...s, items: s.items.filter((_, j) => j !== itemIndex) } : s,
      ),
    );
  }, []);

  const reorderItems = useCallback((sectionIndex: number, oldIndex: number, newIndex: number) => {
    setSections((prev) =>
      prev.map((s, i) =>
        i === sectionIndex ? { ...s, items: arrayMove(s.items, oldIndex, newIndex) } : s,
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

  async function handleDelete() {
    await deleteMutation.mutateAsync(collectionId);
    onNavigate("/collections");
  }

  // Saving is three writes; the page is busy while any of them is in flight.
  const isSaving = [updateMutation, sectionsMutation, configMutation].some((m) => m.isPending);

  // The two authorities the server resolved. A reader who may not edit gets the
  // notice below instead of the form; Delete needs the stronger of the two.
  const viewOnly = isViewOnly(coll);
  const canManage = coll?.can_manage === true;

  if (isLoading) {
    return <p className="py-12 text-center text-sm text-muted-foreground">Loading...</p>;
  }

  if (viewOnly) {
    return <ViewOnlyNotice ownerEmail={ownerLabel(coll)} onBack={onBack} />;
  }

  return (
    <div className="max-w-3xl space-y-6">
      <PageHeader
        onBack={onBack}
        title={headerTitle(coll)}
        actions={
          <EditorHeaderActions
            canManage={canManage}
            canSave={name.length > 0}
            isSaving={isSaving}
            onDelete={() => setDeleteOpen(true)}
            onSave={() => void handleSave()}
          />
        }
      />

      <div className="space-y-1.5">
        <Label htmlFor="collection-name">Name</Label>
        <Input
          id="collection-name"
          type="text"
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="Collection name"
        />
      </div>

      {/* MarkdownEditor sizes itself to its parent, so it gets a plain block. */}
      <div className="space-y-1.5">
        <Label>Description</Label>
        <MarkdownEditor
          value={description}
          onChange={setDescription}
          placeholder="Describe this collection (supports markdown)..."
          minHeight="200px"
        />
      </div>

      <SectionCard title="Settings">
        <div className="flex items-center justify-between gap-4">
          <div>
            <span className="text-sm font-medium">Thumbnail Size</span>
            <p className="text-xs text-muted-foreground">
              Controls how asset thumbnails display in the collection viewer
            </p>
          </div>
          <Select
            value={config.thumbnail_size || "large"}
            onValueChange={(v) =>
              setConfig({ ...config, thumbnail_size: v as CollectionConfig["thumbnail_size"] })
            }
          >
            <SelectTrigger size="sm" aria-label="Thumbnail size" className="w-44">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {THUMBNAIL_SIZES.map((o) => (
                <SelectItem key={o.value} value={o.value}>
                  {o.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
      </SectionCard>

      <div>
        <div className="mb-3 flex items-center justify-between">
          <span className="text-sm font-medium">Sections</span>
          <Button variant="ghost" size="xs" onClick={addSection}>
            <Plus />
            Add Section
          </Button>
        </div>

        <DndContext
          sensors={sectionSensors}
          collisionDetection={closestCenter}
          onDragEnd={handleSectionDragEnd}
        >
          <SortableContext
            items={sections.map((s) => s.id)}
            strategy={verticalListSortingStrategy}
          >
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
          <EmptyState
            action={
              <Button variant="outline" size="sm" onClick={addSection}>
                <Plus />
                Add Section
              </Button>
            }
          >
            No sections yet.
          </EmptyState>
        )}
      </div>

      <ConfirmDialog
        open={deleteOpen}
        onOpenChange={setDeleteOpen}
        title="Delete collection"
        description="This cannot be undone. The assets inside are not deleted."
        confirmLabel="Delete"
        destructive
        loading={deleteMutation.isPending}
        onConfirm={handleDelete}
      />
    </div>
  );
}

/**
 * The collection this editor belongs to, as it was loaded. The name field
 * below edits the name, so the header deliberately does not track it — a
 * header that renamed itself keystroke by keystroke would leave the reader
 * with no record of what they opened.
 */
function headerTitle(coll: { name: string } | undefined): string {
  return coll?.name || "Untitled Collection";
}

/** The assets the section builder can choose from, empty until they load. */
function assetList(data: { data?: Asset[] } | undefined): Asset[] {
  return data?.data ?? [];
}

/** True once a collection has loaded and it grants no edit rights. */
function isViewOnly(coll: { can_edit?: boolean } | undefined): boolean {
  return coll !== undefined && coll.can_edit !== true;
}

/** Who to ask for an Editor share, named when the record knows. */
function ownerLabel(coll: { owner_email?: string } | undefined): string {
  return coll?.owner_email || "its owner";
}
