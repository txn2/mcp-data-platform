import { useState } from "react";
import { FolderOpen } from "lucide-react";
import type { Prompt } from "@/api/admin/types";
import { usePromptCollections, useAssignPromptCollection } from "@/api/portal/hooks";

// CollectionPicker assigns a prompt to a collection (#1010). Rendered only
// for callers who may organize the prompt (its owner, or an admin for shared
// prompts); the server enforces the same rule.
export function CollectionPicker({ prompt }: { prompt: Prompt }) {
  const { data } = usePromptCollections();
  const assign = useAssignPromptCollection();
  const [error, setError] = useState<string | null>(null);

  const collections = data?.data ?? [];

  function handleChange(collectionId: string) {
    setError(null);
    assign.mutate(
      { id: prompt.id, collectionId },
      { onError: (err) => setError(err instanceof Error ? err.message : "Assignment failed") },
    );
  }

  return (
    <div className="inline-flex items-center gap-2">
      <FolderOpen className="h-3.5 w-3.5 text-muted-foreground" />
      <label className="text-xs text-muted-foreground" htmlFor="prompt-collection">Collection</label>
      <select
        id="prompt-collection"
        value={prompt.collection_id ?? ""}
        onChange={(e) => handleChange(e.target.value)}
        disabled={assign.isPending}
        className="rounded-md border bg-background px-2 py-1 text-xs outline-none disabled:opacity-50"
      >
        <option value="">None</option>
        {collections.map((c) => (
          <option key={c.id} value={c.id}>{c.name}</option>
        ))}
      </select>
      {error && <span className="text-xs text-red-400">{error}</span>}
    </div>
  );
}
