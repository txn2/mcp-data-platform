import { useState } from "react";
import { FolderOpen } from "lucide-react";
import type { Prompt } from "@/api/admin/types";
import { usePromptCollections, useAssignPromptCollection } from "@/api/portal/hooks";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";

// NO_COLLECTION is "not in any collection". The API takes the empty string for
// it, which a Select item cannot carry, so it travels under a sentinel.
const NO_COLLECTION = "__none__";

// CollectionPicker assigns a prompt to a collection (#1010). Rendered only
// for callers who may organize the prompt (its owner, or an admin for shared
// prompts); the server enforces the same rule.
export function CollectionPicker({ prompt }: { prompt: Prompt }) {
  const { data } = usePromptCollections();
  const assign = useAssignPromptCollection();
  const [error, setError] = useState<string | null>(null);

  const collections = data?.data ?? [];

  function handleChange(value: string) {
    setError(null);
    const collectionId = value === NO_COLLECTION ? "" : value;
    assign.mutate(
      { id: prompt.id, collectionId },
      { onError: (err) => setError(err instanceof Error ? err.message : "Assignment failed") },
    );
  }

  return (
    <div className="inline-flex items-center gap-2">
      <FolderOpen className="size-3.5 text-muted-foreground" />
      <Label htmlFor="prompt-collection" className="text-xs text-muted-foreground">
        Collection
      </Label>
      <Select
        value={prompt.collection_id || NO_COLLECTION}
        onValueChange={handleChange}
        disabled={assign.isPending}
      >
        <SelectTrigger id="prompt-collection" size="sm" className="text-xs">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value={NO_COLLECTION} className="text-xs">None</SelectItem>
          {collections.map((c) => (
            <SelectItem key={c.id} value={c.id} className="text-xs">{c.name}</SelectItem>
          ))}
        </SelectContent>
      </Select>
      {error && <span className="text-xs text-destructive">{error}</span>}
    </div>
  );
}
