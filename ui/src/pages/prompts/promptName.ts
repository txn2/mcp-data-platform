// Prompt name rules mirrored from the backend (pkg/prompt/prompt.go,
// ValidateName / maxNameLength / validNamePattern). Keep these in sync with
// that file so the form rejects exactly what the server rejects, before a
// round-trip.
export const PROMPT_NAME_MAX_LENGTH = 128;
export const PROMPT_NAME_PATTERN = /^[a-z0-9][a-z0-9_-]*$/;

// validatePromptName returns a human-readable error when the name is invalid,
// or null when it is acceptable.
export function validatePromptName(name: string): string | null {
  if (!name) return "Name is required.";
  if (name.length > PROMPT_NAME_MAX_LENGTH) {
    return `Name must be at most ${PROMPT_NAME_MAX_LENGTH} characters.`;
  }
  if (!PROMPT_NAME_PATTERN.test(name)) {
    return "Use lowercase letters, digits, hyphens, and underscores; must start with a letter or digit.";
  }
  return null;
}

// isPromptNameConflict reports whether a save error is the server's
// duplicate-name (409) response, so callers can surface it on the name field
// rather than as a generic banner.
export function isPromptNameConflict(message: string): boolean {
  return /already exists/i.test(message);
}
