import { useId } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";

/** The sidebar's edit mode: an asset's name, description and tags. */
export function AssetMetadataForm({
  name,
  description,
  tags,
  onNameChange,
  onDescriptionChange,
  onTagsChange,
  onSave,
  onCancel,
  saving,
}: {
  name: string;
  description: string;
  /** Tags as the comma-separated string the field edits. */
  tags: string;
  onNameChange: (v: string) => void;
  onDescriptionChange: (v: string) => void;
  onTagsChange: (v: string) => void;
  onSave: () => void;
  onCancel: () => void;
  saving: boolean;
}) {
  const id = useId();
  return (
    <div className="space-y-3">
      <div className="space-y-1.5">
        <Label htmlFor={`${id}-name`} className="text-xs text-muted-foreground">
          Name
        </Label>
        <Input id={`${id}-name`} type="text" value={name} onChange={(e) => onNameChange(e.target.value)} />
      </div>
      <div className="space-y-1.5">
        <Label htmlFor={`${id}-desc`} className="text-xs text-muted-foreground">
          Description
        </Label>
        <Textarea
          id={`${id}-desc`}
          value={description}
          onChange={(e) => onDescriptionChange(e.target.value)}
          rows={3}
          // ui/textarea sizes to its content unless a fixed height is asked for.
          className="field-sizing-fixed resize-none"
        />
      </div>
      <div className="space-y-1.5">
        <Label htmlFor={`${id}-tags`} className="text-xs text-muted-foreground">
          Tags (comma-separated)
        </Label>
        <Input id={`${id}-tags`} type="text" value={tags} onChange={(e) => onTagsChange(e.target.value)} />
      </div>
      <div className="flex gap-2">
        <Button size="sm" onClick={onSave} disabled={saving}>
          Save
        </Button>
        <Button variant="secondary" size="sm" onClick={onCancel}>
          Cancel
        </Button>
      </div>
    </div>
  );
}
