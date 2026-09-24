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
 */
export function DestinationPicker({
  choices,
  value,
  onChange,
  disabled,
}: {
  choices: MoveTarget[];
  value: string;
  onChange: (key: string) => void;
  disabled: boolean;
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
    </div>
  );
}
