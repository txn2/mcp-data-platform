import { useState } from "react";
import { useCreateDefinition, type Definition, type DefinitionKind } from "@/api/portal/catalog";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { CancelButton, MutationError, SaveButton } from "./primitives";
import { KIND_NAMES } from "./utils";

/**
 * CreateDefinitionForm creates a definition of one of the kinds offered, with a
 * description when one is written, filed under parent when one is given. A
 * definition of the same kind whose name gives the same slug is updated rather
 * than duplicated, which the platform decides.
 */
export function CreateDefinitionForm({
  kinds,
  parent,
  onDone,
  onCancel,
}: {
  kinds: DefinitionKind[];
  parent?: string;
  onDone: (def: Definition) => void;
  onCancel: () => void;
}) {
  const create = useCreateDefinition();
  const [kind, setKind] = useState<DefinitionKind>(kinds[0] ?? "tag");
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const kindName = KIND_NAMES[kind];

  return (
    <div className="space-y-2 rounded-xl border bg-card p-3">
      <div className="flex flex-wrap gap-2">
        {kinds.length > 1 && (
          <Select value={kind} onValueChange={(v) => setKind(v as DefinitionKind)}>
            <SelectTrigger aria-label="Kind" className="w-44">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {kinds.map((k) => (
                <SelectItem key={k} value={k}>
                  {KIND_NAMES[k]}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        )}
        <Input
          aria-label={`New ${kindName} name`}
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder={`Name of the ${kindName}`}
          className="flex-1"
        />
      </div>
      <Textarea
        aria-label={`New ${kindName} description`}
        value={description}
        onChange={(e) => setDescription(e.target.value)}
        placeholder="What it means (markdown, optional)"
        rows={3}
      />
      <div className="flex gap-2">
        <SaveButton
          disabled={create.isPending || !name.trim()}
          onClick={() =>
            create.mutate(
              { kind, name: name.trim(), parent, description: description.trim() || undefined },
              { onSuccess: onDone },
            )
          }
        />
        <CancelButton onClick={onCancel} />
      </div>
      <MutationError mut={create} />
    </div>
  );
}
