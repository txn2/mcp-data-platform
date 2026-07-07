import { useState, useCallback } from "react";
import { X, Loader2 } from "lucide-react";
import { useUpdateResource } from "@/api/resources/hooks";
import { parseTags } from "@/lib/tags";
import type { Resource, ResourceUpdate } from "@/api/resources/types";
import { CATEGORIES } from "./shared";
import { Overlay } from "./Overlay";

export function EditModal({ resource: r, onClose }: { resource: Resource; onClose: () => void }) {
  const update = useUpdateResource();
  const [displayName, setDisplayName] = useState(r.display_name);
  const [description, setDescription] = useState(r.description);
  const [tagsInput, setTagsInput] = useState(r.tags.join(", "));
  const [cat, setCat] = useState(r.category);
  const [error, setError] = useState("");

  const handleSave = useCallback(async () => {
    const upd: ResourceUpdate = {};
    if (displayName.trim() !== r.display_name) upd.display_name = displayName.trim();
    if (description.trim() !== r.description) upd.description = description.trim();
    const tags = parseTags(tagsInput);
    if (JSON.stringify(tags) !== JSON.stringify(r.tags)) upd.tags = tags;
    if (cat !== r.category) upd.category = cat;

    if (Object.keys(upd).length === 0) { onClose(); return; }

    try {
      await update.mutateAsync({ id: r.id, update: upd });
      onClose();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Update failed");
    }
  }, [displayName, description, tagsInput, cat, r, update, onClose]);

  return (
    <Overlay onClose={onClose}>
      <div className="bg-card rounded-lg border shadow-lg w-full p-6 space-y-4">
        <div className="flex items-center justify-between">
          <h2 className="text-lg font-semibold">Edit Resource</h2>
          <button onClick={onClose} className="rounded p-1 hover:bg-muted"><X className="h-4 w-4" /></button>
        </div>

        {error && <p className="text-sm text-destructive bg-destructive/10 rounded-md px-3 py-2">{error}</p>}

        <label className="block space-y-1">
          <span className="text-xs font-medium text-muted-foreground">Display Name</span>
          <input value={displayName} onChange={(e) => setDisplayName(e.target.value)} className="w-full rounded-md border bg-background px-3 py-2 text-sm outline-none ring-ring focus:ring-2" />
        </label>

        <label className="block space-y-1">
          <span className="text-xs font-medium text-muted-foreground">Description</span>
          <textarea value={description} onChange={(e) => setDescription(e.target.value)} rows={3} className="w-full rounded-md border bg-background px-3 py-2 text-sm outline-none ring-ring focus:ring-2 resize-none" />
        </label>

        <label className="block space-y-1">
          <span className="text-xs font-medium text-muted-foreground">Category</span>
          <select value={cat} onChange={(e) => setCat(e.target.value)} className="w-full rounded-md border bg-background px-3 py-2 text-sm">
            {CATEGORIES.map((c) => <option key={c} value={c}>{c}</option>)}
            {!CATEGORIES.includes(cat as typeof CATEGORIES[number]) && <option value={cat}>{cat}</option>}
          </select>
        </label>

        <label className="block space-y-1">
          <span className="text-xs font-medium text-muted-foreground">Tags (comma-separated)</span>
          <input value={tagsInput} onChange={(e) => setTagsInput(e.target.value)} className="w-full rounded-md border bg-background px-3 py-2 text-sm outline-none ring-ring focus:ring-2" />
        </label>

        <div className="flex justify-end gap-2 pt-2">
          <button onClick={onClose} className="rounded-md border px-4 py-2 text-sm hover:bg-muted transition-colors">Cancel</button>
          <button
            onClick={handleSave}
            disabled={update.isPending}
            className="inline-flex items-center gap-1.5 rounded-md bg-primary px-4 py-2 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50 transition-colors"
          >
            {update.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            Save
          </button>
        </div>
      </div>
    </Overlay>
  );
}
