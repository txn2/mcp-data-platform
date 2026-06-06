import { useState } from "react";
import { parseTags } from "@/lib/tags";

// TagsField is a comma-separated tags input. It keeps the raw typed string in
// local state (so a trailing comma/space while typing is not stripped under the
// cursor) and emits the parsed, de-duplicated tag list on every change.
export function TagsField({ tags, onChange }: { tags: string[]; onChange: (tags: string[]) => void }) {
  const [raw, setRaw] = useState(tags.join(", "));
  return (
    <div>
      <label className="text-xs text-muted-foreground">Tags</label>
      <input
        value={raw}
        onChange={(e) => {
          setRaw(e.target.value);
          onChange(parseTags(e.target.value));
        }}
        placeholder="comma, separated, tags"
        className="w-full rounded-md border bg-background px-3 py-1.5 text-sm outline-none"
      />
    </div>
  );
}
