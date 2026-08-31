import { useId, useState } from "react";
import { FolderPlus, List } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectSeparator,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { cn } from "@/lib/utils";
import { pathProblem } from "./pathRules";

/**
 * Seed folder names offered for the first segment of a path.
 *
 * They are suggestions, not a closed set: the six were the whole vocabulary a
 * resource could be filed under before folders existed (#1529), and they are
 * kept because they are a reasonable place to start, not because a library has
 * to use them.
 */
export const SEED_FOLDERS = [
  "data",
  "visual",
  "samples",
  "playbooks",
  "templates",
  "references",
] as const;

/**
 * What each seed folder is for, shown while choosing one so a library stays
 * sorted by how the agent is meant to use a file rather than by what kind of
 * file it happens to be.
 *
 * `data` and `visual` are the two that do not describe prose. Without them a
 * stored dataset and a logo both had to be filed under a name that means "a
 * document to read".
 */
export const SEED_FOLDER_HINTS: Record<string, string> = {
  data: "Records the agent reads as facts, not as examples: rosters, mappings, rate tables.",
  visual: "Logos, photographs, diagrams, and design elements meant to be displayed.",
  samples: "Example payloads and extracts the agent can pattern-match against.",
  playbooks: "Step-by-step procedures the agent should follow, not summarize.",
  templates: "Layouts a deliverable must be produced in, used verbatim.",
  references: "Data dictionaries, standards, and background documents to consult.",
};

/** The hint for a path, read off its first folder. */
export function pathHint(path: string): string | undefined {
  return SEED_FOLDER_HINTS[path.split("/")[0] ?? ""];
}

/** The listbox entry that swaps the control over to a typed folder name. */
const NEW_FOLDER = "__new__";

/**
 * folderChoices is what the listbox offers: the folders already in this
 * library, and the seeds, deduplicated and in one order.
 *
 * The value in hand is added back when it is not among them, so a control
 * arriving on a folder that no longer exists — or on one the caller typed a
 * moment ago — still shows what it holds rather than an empty trigger.
 */
function folderChoices(folders: string[], value: string): string[] {
  const all = new Set<string>([...folders, ...SEED_FOLDERS]);
  if (value) all.add(value);
  return [...all].sort((a, b) => a.localeCompare(b));
}

/**
 * A folder path: chosen from the folders that exist, or typed.
 *
 * It was a text input with a `datalist` attached, which is a control that shows
 * nothing until a prefix is typed that matches something. In practice the
 * folders in a library were invisible at the moment of filing into one, so
 * people guessed and a library grew three spellings of the same folder (#1553).
 *
 * So the listbox is the default and typing is the deliberate second act: the
 * folders are on screen before anything is typed, and the last entry is the one
 * that opens a text field for a folder that does not exist yet. A folder is
 * still created by filing something into it, so the typed path has to stay
 * reachable — it is the ordering that changed, not the capability.
 */
export function PathField({
  label = "Folder",
  value,
  onChange,
  folders,
  disabled,
  autoFocus,
  startTyping = false,
}: {
  label?: string;
  value: string;
  onChange: (value: string) => void;
  /** The folders already in this library, offered as the listbox's entries. */
  folders: string[];
  disabled?: boolean;
  autoFocus?: boolean;
  /**
   * Opens the control on the text field rather than the listbox, for a caller
   * whose whole job is naming a folder that does not exist yet — renaming one
   * is the case, where the existing names are what the new one must not be.
   */
  startTyping?: boolean;
}) {
  const id = useId();
  const [typing, setTyping] = useState(startTyping);
  const problem = value === "" ? null : pathProblem(value);
  const hint = pathHint(value);

  return (
    <div className="space-y-1">
      <div className="flex items-center justify-between gap-2">
        <Label htmlFor={`${id}-path`} className="text-xs text-muted-foreground">
          {label}
        </Label>
        {/* The way back. Without it, choosing the new-folder entry once would
            leave the library's own folders unreachable for the rest of the
            dialog. */}
        {typing && (
          <Button
            type="button"
            variant="ghost"
            size="xs"
            disabled={disabled}
            onClick={() => {
              setTyping(false);
              onChange("");
            }}
          >
            <List />
            Choose an existing folder
          </Button>
        )}
      </div>

      {typing ? (
        <Input
          id={`${id}-path`}
          value={value}
          disabled={disabled}
          autoFocus={autoFocus ?? true}
          // Typing is where a path is made, so what is typed is what is sent:
          // the casing and separators a person types on the way to a legal path
          // are theirs to finish, and rewriting mid-keystroke moves the cursor.
          onChange={(e) => onChange(e.target.value)}
          placeholder="data/media-manager/shows"
          aria-invalid={problem !== null}
          aria-describedby={problem ? `${id}-problem` : undefined}
        />
      ) : (
        <FolderListbox
          id={`${id}-path`}
          label={label}
          value={value}
          folders={folders}
          disabled={disabled}
          onChange={onChange}
          onNewFolder={() => {
            setTyping(true);
            onChange("");
          }}
        />
      )}

      {problem ? (
        <p id={`${id}-problem`} data-testid="path-problem" className="text-xs text-destructive">
          {problem}
        </p>
      ) : (
        hint && (
          <p data-testid="path-hint" className={cn("text-xs text-muted-foreground")}>
            {hint}
          </p>
        )
      )}
    </div>
  );
}

/** The folders that exist, as a listbox, with the way out to a new one. */
function FolderListbox({
  id,
  label,
  value,
  folders,
  disabled,
  onChange,
  onNewFolder,
}: {
  id: string;
  label: string;
  value: string;
  folders: string[];
  disabled?: boolean;
  onChange: (value: string) => void;
  onNewFolder: () => void;
}) {
  return (
    <Select
      value={value}
      disabled={disabled}
      onValueChange={(v) => (v === NEW_FOLDER ? onNewFolder() : onChange(v))}
    >
      <SelectTrigger id={id} aria-label={label} className="w-full">
        <SelectValue placeholder="Choose a folder" />
      </SelectTrigger>
      <SelectContent>
        {folderChoices(folders, value).map((f) => (
          <SelectItem key={f} value={f}>
            {f}
          </SelectItem>
        ))}
        <SelectSeparator />
        <SelectItem value={NEW_FOLDER}>
          <FolderPlus />
          New folder...
        </SelectItem>
      </SelectContent>
    </Select>
  );
}
