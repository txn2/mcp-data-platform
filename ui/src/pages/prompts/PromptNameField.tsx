import { cn } from "@/lib/utils";
import { validatePromptName, PROMPT_NAME_MAX_LENGTH } from "./promptName";

interface PromptNameFieldProps {
  value: string;
  onChange: (value: string) => void;
  // serverError is a name-specific error from a failed save (e.g. a duplicate
  // name). Shown only when the typed value is otherwise locally valid, since a
  // local format error is the more immediate thing to fix.
  serverError?: string | null;
}

const NAME_HELP = "Lowercase letters, digits, hyphens, and underscores; must start with a letter or digit.";

// PromptNameField renders the prompt name input with inline validation that
// mirrors the server rule, plus helper text. Shared by the create and edit
// forms so they enforce the name format identically.
export function PromptNameField({ value, onChange, serverError }: PromptNameFieldProps) {
  const formatError = value ? validatePromptName(value) : null;
  const error = formatError ?? serverError ?? null;
  return (
    <div>
      <label className="text-xs text-muted-foreground">Name</label>
      <input
        value={value}
        onChange={(e) => onChange(e.target.value)}
        maxLength={PROMPT_NAME_MAX_LENGTH}
        placeholder="my-prompt"
        aria-invalid={error ? true : undefined}
        className={cn(
          "w-full rounded-md border bg-background px-3 py-1.5 text-sm outline-none",
          error && "border-red-500/60",
        )}
      />
      <p className={cn("text-[11px] mt-1", error ? "text-red-400" : "text-muted-foreground")}>
        {error ?? NAME_HELP}
      </p>
    </div>
  );
}
