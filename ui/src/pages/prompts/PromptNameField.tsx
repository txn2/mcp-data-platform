import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";
import { Field } from "./primitives";
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
    <Field
      id="prompt-name"
      label="Name"
      hint={
        <p className={cn("text-[11px]", error ? "text-destructive" : "text-muted-foreground")}>
          {error ?? NAME_HELP}
        </p>
      }
    >
      <Input
        id="prompt-name"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        maxLength={PROMPT_NAME_MAX_LENGTH}
        placeholder="my-prompt"
        aria-invalid={error ? true : undefined}
      />
    </Field>
  );
}
