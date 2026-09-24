import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { libraryCopy, targetKey, type MoveTarget } from "../scopes";

/**
 * Which library an upload lands in, for a view that names none.
 *
 * The audience line under it is the point: "Mine" and a persona's library are
 * one click apart, and the difference between them is who else can read the
 * file. A picker that stated only the names would make that difference
 * invisible at the moment it is chosen.
 *
 * A persona library the caller belongs to and cannot upload into is named
 * under it, with the role that grants the upload and the move that gets a file
 * there without it (#1866): silently leaving it out gave the caller no way to
 * tell a permission boundary from a missing feature.
 */
export function DestinationPicker({
  choices,
  value,
  onChange,
  disabled,
  withheld = [],
}: {
  choices: MoveTarget[];
  value: string;
  onChange: (key: string) => void;
  disabled: boolean;
  /** Persona libraries the caller belongs to and may not upload into. */
  withheld?: string[];
}) {
  const picked = choices.find((c) => targetKey(c) === value);
  return (
    <div className="space-y-1" data-testid="upload-destination-picker">
      <Label className="text-xs text-muted-foreground">Destination</Label>
      <Select value={value} onValueChange={onChange} disabled={disabled}>
        <SelectTrigger aria-label="Destination" className="w-full">
          <SelectValue placeholder="Choose a library" />
        </SelectTrigger>
        <SelectContent>
          {choices.map((c) => (
            <SelectItem key={targetKey(c)} value={targetKey(c)}>
              {c.label}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      {picked && (
        <p className="text-xs text-muted-foreground">{libraryCopy(picked).audience}</p>
      )}
      {withheld.map((name) => (
        <p key={name} className="text-xs text-muted-foreground" data-testid="upload-withheld-persona">
          The {name} persona library is not offered: adding a file to it takes a role ending in{" "}
          <code className="font-mono">persona-admin:{name}</code>. Upload it here, then open it and move
          it with Edit details &gt; Library.
        </p>
      ))}
    </div>
  );
}
