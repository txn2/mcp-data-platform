import { useCallback, useId, useState } from "react";
import { X, Loader2 } from "lucide-react";
import { useUpdateResource } from "@/api/resources/hooks";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
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
  const ids = useId();

  // A deployment may have filed this resource under a category of its own, so
  // the list offers the built-ins plus whatever the resource arrived with. It
  // is keyed off the stored category rather than the live selection: derived
  // from the selection, picking a built-in would drop the custom one from the
  // list and leave no way back to it.
  const categories = CATEGORIES.includes(r.category as (typeof CATEGORIES)[number])
    ? [...CATEGORIES]
    : [...CATEGORIES, r.category];

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
      <div className="w-full space-y-4 rounded-lg border bg-card p-6 shadow-lg">
        <div className="flex items-center justify-between">
          <h2 className="text-lg font-semibold">Edit Resource</h2>
          <Button variant="ghost" size="icon-sm" onClick={onClose} aria-label="Close">
            <X />
          </Button>
        </div>

        {error && (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}

        <div className="space-y-1">
          <Label htmlFor={`${ids}-name`} className="text-xs text-muted-foreground">
            Display Name
          </Label>
          <Input
            id={`${ids}-name`}
            value={displayName}
            onChange={(e) => setDisplayName(e.target.value)}
          />
        </div>

        <div className="space-y-1">
          <Label htmlFor={`${ids}-description`} className="text-xs text-muted-foreground">
            Description
          </Label>
          <Textarea
            id={`${ids}-description`}
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            rows={3}
            className="field-sizing-fixed resize-none"
          />
        </div>

        <div className="space-y-1">
          <Label className="text-xs text-muted-foreground">Category</Label>
          <Select value={cat} onValueChange={setCat}>
            <SelectTrigger aria-label="Category" className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {categories.map((c) => (
                <SelectItem key={c} value={c}>
                  {c}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>

        <div className="space-y-1">
          <Label htmlFor={`${ids}-tags`} className="text-xs text-muted-foreground">
            Tags (comma-separated)
          </Label>
          <Input
            id={`${ids}-tags`}
            value={tagsInput}
            onChange={(e) => setTagsInput(e.target.value)}
          />
        </div>

        <div className="flex justify-end gap-2 pt-2">
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={handleSave} disabled={update.isPending}>
            {update.isPending && <Loader2 className="animate-spin" />}
            Save
          </Button>
        </div>
      </div>
    </Overlay>
  );
}
