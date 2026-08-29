import { useId } from "react";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
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

/**
 * A folder path, typed, with the folders already in the library offered
 * alongside.
 *
 * A datalist rather than a select: a folder is created by filing something in
 * it, so the destination has to be typeable. Offering the folders that exist is
 * what keeps a library from growing three spellings of the same one.
 */
export function PathField({
  label = "Folder",
  value,
  onChange,
  folders,
  disabled,
  autoFocus,
}: {
  label?: string;
  value: string;
  onChange: (value: string) => void;
  /** The folders already in this library, offered as completions. */
  folders: string[];
  disabled?: boolean;
  autoFocus?: boolean;
}) {
  const id = useId();
  const problem = value === "" ? null : pathProblem(value);
  const hint = pathHint(value);
  const options = [...new Set([...folders, ...SEED_FOLDERS])].sort((a, b) => a.localeCompare(b));

  return (
    <div className="space-y-1">
      <Label htmlFor={`${id}-path`} className="text-xs text-muted-foreground">
        {label}
      </Label>
      <Input
        id={`${id}-path`}
        list={`${id}-folders`}
        value={value}
        disabled={disabled}
        autoFocus={autoFocus}
        // Typing is where a path is made, so what is typed is what is sent: the
        // casing and separators a person types on the way to a legal path are
        // theirs to finish, and rewriting mid-keystroke moves the cursor.
        onChange={(e) => onChange(e.target.value)}
        placeholder="data/media-manager/shows"
        aria-invalid={problem !== null}
        aria-describedby={problem ? `${id}-problem` : undefined}
      />
      <datalist id={`${id}-folders`}>
        {options.map((f) => (
          <option key={f} value={f} />
        ))}
      </datalist>
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
