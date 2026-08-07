import { useState } from "react";
import { Input } from "@/components/ui/input";
import { parseTags } from "@/lib/tags";
import { Field } from "./primitives";

// TagsField is a comma-separated tags input. It keeps the raw typed string in
// local state (so a trailing comma/space while typing is not stripped under the
// cursor) and emits the parsed, de-duplicated tag list on every change.
export function TagsField({ tags, onChange }: { tags: string[]; onChange: (tags: string[]) => void }) {
  const [raw, setRaw] = useState(tags.join(", "));
  return (
    <Field id="prompt-tags" label="Tags">
      <Input
        id="prompt-tags"
        value={raw}
        onChange={(e) => {
          setRaw(e.target.value);
          onChange(parseTags(e.target.value));
        }}
        placeholder="comma, separated, tags"
      />
    </Field>
  );
}
