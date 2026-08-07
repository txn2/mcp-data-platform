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
import { AssetPreviewModal } from "@/components/AssetPreviewModal";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { MarkdownEditor } from "@/components/MarkdownEditor";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { cn } from "@/lib/utils";
import { SortableItem } from "./SortableItem";
import { AssetBrowserModal } from "./AssetBrowserModal";
import type { SectionDraft, ItemDraft } from "./types";

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
  onUpdate: (index: number, field: "title" | "description", value: string) => void;
  onRemove: (index: number) => void;
  onAddItem: (
    sectionIndex: number,
    assetId: string,
    assetName: string,
    assetContentType: string,
  ) => void;
  onRemoveItem: (sectionIndex: number, itemIndex: number) => void;
  onReorderItems: (sectionIndex: number, oldIndex: number, newIndex: number) => void;
  assets: Asset[];
}) {
  const { attributes, listeners, setNodeRef, transform, transition } = useSortable({
    id: section.id,
  });
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
    <Card ref={setNodeRef} style={style} className="gap-0 overflow-hidden py-0">
      {/* Header — always visible, acts as collapse toggle */}
      <div className="flex items-center gap-2 bg-muted/20 px-4 py-3">
        <Button
          {...attributes}
          {...listeners}
          variant="ghost"
          size="icon-xs"
          title="Drag to reorder"
          className="cursor-grab text-muted-foreground"
        >
          <GripVertical />
        </Button>
        <button
          type="button"
          onClick={() => setCollapsed((c) => !c)}
          aria-expanded={!collapsed}
          className="flex flex-1 items-center gap-2 text-left"
        >
          <ChevronDown
            className={cn(
              "size-3.5 text-muted-foreground transition-transform",
              collapsed && "-rotate-90",
            )}
          />
          <span className="truncate text-sm font-medium">{displayTitle}</span>
          <span className="text-xs text-muted-foreground">
            {itemCount} {itemCount === 1 ? "asset" : "assets"}
          </span>
        </button>
        <Button
          variant="ghost"
          size="icon-xs"
          onClick={() => setConfirmDelete(true)}
          title="Remove section"
          className="text-muted-foreground hover:text-destructive"
        >
          <Trash2 />
        </Button>
      </div>

      {!collapsed && (
        <div className="space-y-3 border-t p-4">
          <Input
            type="text"
            value={section.title}
            onChange={(e) => onUpdate(index, "title", e.target.value)}
            aria-label="Section title"
            placeholder="Section title"
          />

          {/* MarkdownEditor sizes itself to its parent, so it gets a plain block. */}
          <div className="space-y-1.5">
            <Label className="text-xs text-muted-foreground">Description (markdown)</Label>
            <MarkdownEditor
              value={section.description}
              onChange={(v) => onUpdate(index, "description", v)}
              placeholder="Section description..."
              minHeight="120px"
            />
          </div>

          <div className="space-y-1.5">
            <Label className="text-xs text-muted-foreground">Assets</Label>
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

          <Button variant="ghost" size="xs" onClick={() => setBrowserOpen(true)}>
            <Plus />
            Browse Assets
          </Button>
        </div>
      )}

      <DeleteSectionConfirm
        open={confirmDelete}
        onOpenChange={setConfirmDelete}
        title={displayTitle}
        itemCount={itemCount}
        onConfirm={() => {
          setConfirmDelete(false);
          onRemove(index);
        }}
      />

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
        <ItemPreview item={itemPreview} onClose={() => setItemPreview(null)} />
      )}
    </Card>
  );
}

/** Deleting a section takes its assets out of the collection with it. */
function DeleteSectionConfirm({
  open,
  onOpenChange,
  title,
  itemCount,
  onConfirm,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  itemCount: number;
  onConfirm: () => void;
}) {
  const assets = itemCount === 1 ? "asset" : "assets";
  return (
    <ConfirmDialog
      open={open}
      onOpenChange={onOpenChange}
      title="Delete Section"
      description={
        <>
          Delete <strong>{title}</strong>?
          {itemCount > 0 && ` This removes ${itemCount} ${assets} from the section.`} This cannot
          be undone.
        </>
      }
      confirmLabel="Delete"
      destructive
      onConfirm={onConfirm}
    />
  );
}

/** A draft item carries only what the collection saved, so the preview falls
 *  back to the asset id and to plain text when a name or type is missing. */
function ItemPreview({ item, onClose }: { item: ItemDraft; onClose: () => void }) {
  return (
    <AssetPreviewModal
      assetId={item.asset_id}
      assetName={item.assetName || item.asset_id}
      contentType={item.assetContentType || "text/plain"}
      onClose={onClose}
    />
  );
}
