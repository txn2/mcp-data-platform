import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  libraryCopy,
  targetKey,
  PERSON_TARGET,
  type MoveTarget,
} from "../scopes";

/**
 * The library a resource is filed in, as an editable field (#1502).
 *
 * A resource's library was chosen once, on the upload form, and never again, so
 * the only route from a personal library to a shared one was to upload the file
 * a second time -- which mints a second id, a second URI and a second blob, and
 * leaves every asset and prompt that referenced the first one referencing it.
 *
 * The options are the libraries this caller may move to and nothing else, so the
 * field never offers a move the server will refuse. The library the resource is
 * in now is always among them even when the caller could not move it there,
 * because it has to be selectable for "leave it where it is" to be expressible.
 */
export function LibraryField({
  id,
  currentKey,
  targets,
  value,
  onChange,
  personEmail,
  onPersonEmailChange,
}: {
  id: string;
  /** targetKey of the library the resource is in now. */
  currentKey: string;
  /** Every option the picker offers, current library included. */
  targets: MoveTarget[];
  /** targetKey of the selected option. */
  value: string;
  onChange: (key: string) => void;
  personEmail: string;
  onPersonEmailChange: (email: string) => void;
}) {
  const selected = targets.find((t) => targetKey(t) === value);
  const namingAPerson = selected?.scope_id === PERSON_TARGET;
  const moving = value !== currentKey;

  return (
    <div className="space-y-1">
      <Label className="text-xs text-muted-foreground">Library</Label>
      <Select value={value} onValueChange={onChange}>
        <SelectTrigger aria-label="Library" className="w-full">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {targets.map((t) => (
            <SelectItem key={targetKey(t)} value={targetKey(t)}>
              {t.label}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>

      {namingAPerson && (
        <>
          {/* Labelled like every other field on this form. A person's library
              is addressed rather than chosen from a list, because the platform
              has no roster of everyone's library to enumerate. */}
          <Label htmlFor={`${id}-person`} className="text-xs text-muted-foreground">
            Person's email
          </Label>
          <Input
            id={`${id}-person`}
            value={personEmail}
            onChange={(e) => onPersonEmailChange(e.target.value)}
            placeholder="person@example.com"
          />
        </>
      )}

      {moving && (
        <p
          data-testid="library-move-note"
          className="text-xs text-muted-foreground"
        >
          {/* Two facts, both of which change what the file is: who can read it,
              and what it is called. The second is the one nobody expects, so it
              says outright that the address already written down keeps working. */}
          {namingAPerson
            ? "Only that person will see it."
            : libraryCopy(selected ?? null).audience}{" "}
          Its URI changes to match, and the URI it is leaving keeps resolving.
        </p>
      )}
    </div>
  );
}
