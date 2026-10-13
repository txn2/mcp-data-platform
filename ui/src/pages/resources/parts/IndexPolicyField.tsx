import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import type { IndexPolicy } from "@/api/resources/types";

/**
 * What search reads of a file under each index policy (#2111), in the words
 * the field and the details panel use.
 */
export const INDEX_POLICY_LABELS: Record<IndexPolicy, string> = {
  full: "Contents",
  metadata: "Details only",
  none: "Not indexed",
};

const INDEX_POLICY_HELP: Record<IndexPolicy, string> = {
  full: "Search reads its name, description, folder, tags and contents.",
  metadata: "Search reads its name, description, folder and tags, not its contents.",
  none: "Not in search one by one. It is still listed in its folder, and search finds the folder.",
};

/** The option a field holds when the person leaves the choice to the rules. */
export const INDEX_POLICY_DEFAULT = "default";

export type IndexChoice = IndexPolicy | typeof INDEX_POLICY_DEFAULT;

/**
 * IndexPolicyField is how a file is indexed for search, as a form field.
 * With allowDefault it offers leaving the choice to the deployment's rules,
 * which is what an upload does unless the person says otherwise.
 */
export function IndexPolicyField({
  id,
  value,
  onChange,
  allowDefault,
}: {
  id: string;
  value: IndexChoice;
  onChange: (value: IndexChoice) => void;
  allowDefault?: boolean;
}) {
  const options: IndexChoice[] = allowDefault
    ? [INDEX_POLICY_DEFAULT, "full", "metadata", "none"]
    : ["full", "metadata", "none"];
  return (
    <div className="space-y-1">
      <Label htmlFor={`${id}-index`} className="text-xs text-muted-foreground">
        Search
      </Label>
      <Select value={value} onValueChange={(v) => onChange(v as IndexChoice)}>
        <SelectTrigger id={`${id}-index`} aria-label="Search" className="w-full">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {options.map((o) => (
            <SelectItem key={o} value={o}>
              {o === INDEX_POLICY_DEFAULT ? "Default" : INDEX_POLICY_LABELS[o]}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      <p className="text-xs text-muted-foreground">
        {value === INDEX_POLICY_DEFAULT
          ? "Decided by this deployment's rules for the folder and kind of file."
          : INDEX_POLICY_HELP[value]}
      </p>
    </div>
  );
}
