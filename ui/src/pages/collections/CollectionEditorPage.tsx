import { useState, useEffect, useCallback, useMemo } from "react";
import { ArrowLeft, Plus, Trash2, GripVertical, Save, FileText, Eye, Search, X, ArrowUpDown, ChevronDown } from "lucide-react";
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
  useSortable,
  verticalListSortingStrategy,
} from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
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
import { AuthImg } from "@/components/AuthImg";
import { AssetPreviewModal } from "@/components/AssetPreviewModal";

interface Props {
  collectionId: string;
  onBack: () => void;
  onNavigate: (path: string) => void;
}

interface SectionDraft {
  id: string;
  title: string;
  description: string;
  items: ItemDraft[];
}

interface ItemDraft {
  id: string;
  asset_id: string;
  assetName?: string;
  assetContentType?: string;
}

let draftIdCounter = 0;
function draftId() {
  return `draft-${++draftIdCounter}`;
}

/** Sortable item card within a section. */
function SortableItem({
  item,
  onRemove,
  onPreview,
}: {
  item: ItemDraft;
  onRemove: () => void;
  onPreview: () => void;
}) {
  const { attributes, listeners, setNodeRef, transform, transition } = useSortable({ id: item.id });
  const [confirmDelete, setConfirmDelete] = useState(false);

  const style = {
    transform: CSS.Transform.toString(transform),
    transition,
  };

  return (
    <div
      ref={setNodeRef}
      style={style}
      className="flex items-center gap-2 rounded border bg-muted/50 px-3 py-1.5 text-sm"
    >
      <button {...attributes} {...listeners} className="cursor-grab text-muted-foreground hover:text-foreground" title="Drag to reorder">
        <GripVertical className="h-3.5 w-3.5" />
      </button>
      <FileText className="h-3.5 w-3.5 text-muted-foreground shrink-0" />
      <span className="flex-1 truncate">{item.assetName || item.asset_id}</span>
      {item.assetContentType && (
        <span className="text-[10px] text-muted-foreground shrink-0">{item.assetContentType}</span>
      )}
      <button onClick={onPreview} className="text-muted-foreground hover:text-foreground shrink-0" title="Preview">
        <Eye className="h-3 w-3" />
      </button>
      {confirmDelete ? (
        <span className="flex items-center gap-1 shrink-0">
          <button onClick={onRemove} className="text-[10px] text-destructive font-medium hover:underline">Remove</button>
          <button onClick={() => setConfirmDelete(false)} className="text-[10px] text-muted-foreground hover:underline">Cancel</button>
        </span>
      ) : (
        <button onClick={() => setConfirmDelete(true)} className="text-muted-foreground hover:text-destructive shrink-0" title="Remove">
          <Trash2 className="h-3 w-3" />
        </button>
      )}
    </div>
  );
}

/** Asset browser modal for adding assets to a section. */
function AssetBrowserModal({
  assets,
  onAdd,
  onClose,
}: {
  assets: Asset[];
  onAdd: (asset: Asset) => void;
  onClose: () => void;
}) {
  const [search, setSearch] = useState("");
  const [sortBy, setSortBy] = useState<"name" | "created_at">("name");
  const [sortDir, setSortDir] = useState<"asc" | "desc">("asc");
  const [previewing, setPreviewing] = useState<Asset | null>(null);

  function toggleSort(col: "name" | "created_at") {
    if (sortBy === col) {
      setSortDir((d) => (d === "asc" ? "desc" : "asc"));
    } else {
      setSortBy(col);
      setSortDir(col === "name" ? "asc" : "desc");
    }
  }

  const filtered = useMemo(() => {
    let list = assets;
    if (search) {
      const q = search.toLowerCase();
      list = list.filter(
        (a) =>
          a.name.toLowerCase().includes(q) ||
          a.description.toLowerCase().includes(q) ||
          a.tags.some((t) => t.toLowerCase().includes(q)),
      );
    }
    list = [...list].sort((a, b) => {
      const cmp = sortBy === "name"
        ? a.name.localeCompare(b.name)
        : new Date(a.created_at).getTime() - new Date(b.created_at).getTime();
      return sortDir === "asc" ? cmp : -cmp;
    });
    return list;
  }, [assets, search, sortBy, sortDir]);

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center">
      <div className="absolute inset-0 bg-black/50" onClick={onClose} role="button" tabIndex={-1} aria-label="Close" onKeyDown={(e) => { if (e.key === "Escape") onClose(); }} />
      <div className="relative rounded-lg border bg-card shadow-lg w-full max-w-2xl mx-4 max-h-[80vh] flex flex-col">
        <div className="flex items-center gap-3 p-4 border-b">
          <h3 className="text-sm font-semibold flex-1">Add Assets</h3>
          <button onClick={onClose} className="rounded-md p-1 hover:bg-accent text-muted-foreground hover:text-foreground">
            <X className="h-4 w-4" />
          </button>
        </div>
        <div className="p-4 pb-2">
          <div className="relative">
            <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
            <input
              type="text"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="Search by name, description, or tag..."
              autoFocus
              className="w-full rounded-md border bg-background pl-9 pr-3 py-2 text-sm outline-none ring-ring focus:ring-2"
            />
          </div>
        </div>
        <div className="flex-1 overflow-auto px-4 pb-4">
          <table className="w-full text-sm">
            <thead className="sticky top-0 bg-card">
              <tr className="border-b">
                <th className="w-10" />
                <th className="py-2 text-left font-medium text-muted-foreground">
                  <button onClick={() => toggleSort("name")} className="flex items-center gap-1 hover:text-foreground">
                    Name <ArrowUpDown className="h-3 w-3" />
                  </button>
                </th>
                <th className="py-2 text-left font-medium text-muted-foreground w-[20%]">Type</th>
                <th className="py-2 text-left font-medium text-muted-foreground w-[25%]">
                  <button onClick={() => toggleSort("created_at")} className="flex items-center gap-1 hover:text-foreground">
                    Created <ArrowUpDown className="h-3 w-3" />
                  </button>
                </th>
                <th className="w-20" />
              </tr>
            </thead>
            <tbody>
              {filtered.map((a) => (
                <tr key={a.id} className="border-b last:border-0 hover:bg-accent/50">
                  <td className="py-2 pr-2">
                    {a.thumbnail_s3_key ? (
                      <AuthImg src={`/api/v1/portal/assets/${a.id}/thumbnail`} alt="" className="w-8 h-6 rounded object-cover" />
                    ) : (
                      <div className="w-8 h-6 rounded bg-muted flex items-center justify-center">
                        <FileText className="h-3 w-3 text-muted-foreground/50" />
                      </div>
                    )}
                  </td>
                  <td className="py-2 max-w-0">
                    <span className="font-medium truncate block">{a.name}</span>
                    {a.tags.length > 0 && (
                      <div className="flex gap-1 mt-0.5">
                        {a.tags.slice(0, 3).map((t) => (
                          <span key={t} className="text-[9px] px-1 py-0.5 rounded bg-muted text-muted-foreground">{t}</span>
                        ))}
                      </div>
                    )}
                  </td>
                  <td className="py-2 text-muted-foreground text-xs">{a.content_type}</td>
                  <td className="py-2 text-muted-foreground text-xs">{new Date(a.created_at).toLocaleDateString()}</td>
                  <td className="py-2">
                    <div className="flex items-center gap-1.5">
                      <button
                        onClick={() => setPreviewing(a)}
                        className="rounded bg-muted text-muted-foreground px-2 py-0.5 text-xs font-medium hover:bg-muted/80 hover:text-foreground"
                        title="Preview asset"
                      >
                        <Eye className="h-3 w-3" />
                      </button>
                      <button
                        onClick={() => onAdd(a)}
                        className="rounded bg-primary/10 text-primary px-2 py-0.5 text-xs font-medium hover:bg-primary/20"
                      >
                        Add
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
              {filtered.length === 0 && (
                <tr>
                  <td colSpan={5} className="py-8 text-center text-muted-foreground text-sm">
                    No assets found
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>
      {previewing && (
        <AssetPreviewModal
          assetId={previewing.id}
          assetName={previewing.name}
          contentType={previewing.content_type}
          sizeBytes={previewing.size_bytes}
          onClose={() => setPreviewing(null)}
        />
      )}
    </div>
  );
}

function SortableSection({
  section,
  index,
  onUpdate,
  onRemove,
  onAddItem,
  onRemoveItem,
  onReorderItems,
  assets,
}: {
  section: SectionDraft;
  index: number;
  onUpdate: (index: number, field: "title" | "description", value: string) => void;
  onRemove: (index: number) => void;
  onAddItem: (sectionIndex: number, assetId: string, assetName: string, assetContentType: string) => void;
  onRemoveItem: (sectionIndex: number, itemIndex: number) => void;
  onReorderItems: (sectionIndex: number, oldIndex: number, newIndex: number) => void;
  assets: Asset[];
}) {
  const { attributes, listeners, setNodeRef, transform, transition } = useSortable({ id: section.id });
  const [browserOpen, setBrowserOpen] = useState(false);
  const [itemPreview, setItemPreview] = useState<ItemDraft | null>(null);
  const [collapsed, setCollapsed] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);

  const sensors = useSensors(
    useSensor(PointerSensor),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }),
  );

  const style = {
    transform: CSS.Transform.toString(transform),
    transition,
  };

  function handleItemDragEnd(event: DragEndEvent) {
    const { active, over } = event;
    if (!over || active.id === over.id) return;
    const oldIdx = section.items.findIndex((i) => i.id === active.id);
    const newIdx = section.items.findIndex((i) => i.id === over.id);
    if (oldIdx !== -1 && newIdx !== -1) {
      onReorderItems(index, oldIdx, newIdx);
    }
  }

  const displayTitle = section.title || "Untitled Section";
  const itemCount = section.items.length;

  return (
    <div ref={setNodeRef} style={style} className="rounded-lg border bg-card overflow-hidden">
      {/* Header — always visible, acts as collapse toggle */}
      <div className="flex items-center gap-2 px-4 py-3 bg-muted/20">
        <button {...attributes} {...listeners} className="cursor-grab text-muted-foreground hover:text-foreground" title="Drag to reorder">
          <GripVertical className="h-4 w-4" />
        </button>
        <button
          type="button"
          onClick={() => setCollapsed((c) => !c)}
          className="flex flex-1 items-center gap-2 text-left"
        >
          <ChevronDown className={`h-3.5 w-3.5 text-muted-foreground transition-transform ${collapsed ? "-rotate-90" : ""}`} />
          <span className="text-sm font-medium truncate">{displayTitle}</span>
          <span className="text-[10px] text-muted-foreground">
            {itemCount} {itemCount === 1 ? "asset" : "assets"}
          </span>
        </button>
        <button
          onClick={() => setConfirmDelete(true)}
          className="text-muted-foreground hover:text-destructive"
          title="Remove section"
        >
          <Trash2 className="h-3.5 w-3.5" />
        </button>
      </div>

      {/* Expandable content */}
      {!collapsed && (
        <div className="p-4 space-y-3 border-t">
          <input
            type="text"
            value={section.title}
            onChange={(e) => onUpdate(index, "title", e.target.value)}
            placeholder="Section title"
            className="w-full rounded-md border bg-background px-3 py-2 text-sm outline-none ring-ring focus:ring-2"
          />

          <div>
            <label className="block text-xs text-muted-foreground mb-1">Description (markdown)</label>
            <MarkdownEditor
              value={section.description}
              onChange={(v) => onUpdate(index, "description", v)}
              placeholder="Section description..."
              minHeight="120px"
            />
          </div>

          {/* Items with drag-and-drop */}
          <div>
            <label className="block text-xs text-muted-foreground mb-1">Assets</label>
            <DndContext sensors={sensors} collisionDetection={closestCenter} onDragEnd={handleItemDragEnd}>
              <SortableContext items={section.items.map((i) => i.id)} strategy={verticalListSortingStrategy}>
                <div className="space-y-1.5">
                  {section.items.map((item, itemIdx) => (
                    <SortableItem
                      key={item.id}
                      item={item}
                      onRemove={() => onRemoveItem(index, itemIdx)}
                      onPreview={() => setItemPreview(item)}
                    />
                  ))}
                </div>
              </SortableContext>
            </DndContext>
          </div>

          <button
            type="button"
            onClick={() => setBrowserOpen(true)}
            className="flex items-center gap-1.5 text-xs text-primary hover:underline"
          >
            <Plus className="h-3 w-3" />
            Browse Assets
          </button>
        </div>
      )}

      {/* Delete confirmation modal */}
      {confirmDelete && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50" onClick={() => setConfirmDelete(false)}>
          <div className="rounded-lg border bg-card p-6 shadow-lg max-w-sm mx-4" onClick={(e) => e.stopPropagation()}>
            <h3 className="text-sm font-semibold mb-2">Delete Section</h3>
            <p className="text-sm text-muted-foreground mb-4">
              Are you sure you want to delete <strong>{displayTitle}</strong>?
              {itemCount > 0 && ` This will remove ${itemCount} ${itemCount === 1 ? "asset" : "assets"} from the section.`}
              {" "}This cannot be undone.
            </p>
            <div className="flex justify-end gap-2">
              <button
                type="button"
                onClick={() => setConfirmDelete(false)}
                className="rounded-md border px-3 py-1.5 text-xs font-medium text-muted-foreground hover:bg-muted"
              >
                Cancel
              </button>
              <button
                type="button"
                onClick={() => {
                  setConfirmDelete(false);
                  onRemove(index);
                }}
                className="rounded-md bg-destructive px-3 py-1.5 text-xs font-medium text-destructive-foreground hover:bg-destructive/90"
              >
                Delete
              </button>
            </div>
          </div>
        </div>
      )}

      {browserOpen && (
        <AssetBrowserModal
          assets={assets}
          onAdd={(a) => {
            onAddItem(index, a.id, a.name, a.content_type);
          }}
          onClose={() => setBrowserOpen(false)}
        />
      )}

      {itemPreview && (
        <AssetPreviewModal
          assetId={itemPreview.asset_id}
          assetName={itemPreview.assetName || itemPreview.asset_id}
          contentType={itemPreview.assetContentType || "text/plain"}
          onClose={() => setItemPreview(null)}
        />
      )}
    </div>
  );
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
