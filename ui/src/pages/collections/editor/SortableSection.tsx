import { useState } from "react";
import { Plus, Trash2, GripVertical, ChevronDown } from "lucide-react";
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
  SortableContext,
  sortableKeyboardCoordinates,
  useSortable,
  verticalListSortingStrategy,
} from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import type { Asset } from "@/api/portal/types";
import { MarkdownEditor } from "@/components/MarkdownEditor";
import { AssetPreviewModal } from "@/components/AssetPreviewModal";
import { SortableItem } from "./SortableItem";
import { AssetBrowserModal } from "./AssetBrowserModal";
import type { SectionDraft, ItemDraft } from "./types";
import { ModalScroll } from "@/components/ModalShell";

export function SortableSection({
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
  onUpdate: (
    index: number,
    field: "title" | "description",
    value: string,
  ) => void;
  onRemove: (index: number) => void;
  onAddItem: (
    sectionIndex: number,
    assetId: string,
    assetName: string,
    assetContentType: string,
  ) => void;
  onRemoveItem: (sectionIndex: number, itemIndex: number) => void;
  onReorderItems: (
    sectionIndex: number,
    oldIndex: number,
    newIndex: number,
  ) => void;
  assets: Asset[];
}) {
  const { attributes, listeners, setNodeRef, transform, transition } =
    useSortable({ id: section.id });
  const [browserOpen, setBrowserOpen] = useState(false);
  const [itemPreview, setItemPreview] = useState<ItemDraft | null>(null);
  const [collapsed, setCollapsed] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);

  const sensors = useSensors(
    useSensor(PointerSensor),
    useSensor(KeyboardSensor, {
      coordinateGetter: sortableKeyboardCoordinates,
    }),
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
    <div
      ref={setNodeRef}
      style={style}
      className="rounded-lg border bg-card overflow-hidden"
    >
      {/* Header — always visible, acts as collapse toggle */}
      <div className="flex items-center gap-2 px-4 py-3 bg-muted/20">
        <button
          {...attributes}
          {...listeners}
          className="cursor-grab text-muted-foreground hover:text-foreground"
          title="Drag to reorder"
        >
          <GripVertical className="h-4 w-4" />
        </button>
        <button
          type="button"
          onClick={() => setCollapsed((c) => !c)}
          className="flex flex-1 items-center gap-2 text-left"
        >
          <ChevronDown
            className={`h-3.5 w-3.5 text-muted-foreground transition-transform ${collapsed ? "-rotate-90" : ""}`}
          />
          <span className="text-sm font-medium truncate">{displayTitle}</span>
          <span className="text-xs text-muted-foreground">
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
            <label className="block text-xs text-muted-foreground mb-1">
              Description (markdown)
            </label>
            <MarkdownEditor
              value={section.description}
              onChange={(v) => onUpdate(index, "description", v)}
              placeholder="Section description..."
              minHeight="120px"
            />
          </div>

          {/* Items with drag-and-drop */}
          <div>
            <label className="block text-xs text-muted-foreground mb-1">
              Assets
            </label>
            <DndContext
              sensors={sensors}
              collisionDetection={closestCenter}
              onDragEnd={handleItemDragEnd}
            >
              <SortableContext
                items={section.items.map((i) => i.id)}
                strategy={verticalListSortingStrategy}
              >
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
        <ModalScroll onClose={() => setConfirmDelete(false)} width="max-w-sm">
          <div className="rounded-lg border bg-card p-6 shadow-lg">
            <h3 className="text-sm font-semibold mb-2">Delete Section</h3>
            <p className="text-sm text-muted-foreground mb-4">
              Are you sure you want to delete <strong>{displayTitle}</strong>?
              {itemCount > 0 &&
                ` This will remove ${itemCount} ${itemCount === 1 ? "asset" : "assets"} from the section.`}{" "}
              This cannot be undone.
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
        </ModalScroll>
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
