import type { Prompt } from "@/api/admin/types";

// extractPromptArguments scans prompt content for placeholders and returns a
// deduplicated, ordered argument list. Existing arguments matched by name
// keep their description and required flag; newly discovered placeholders
// default to required=true. Arguments no longer referenced in the content
// are dropped — content is the source of truth for the set.
//
// Recognized syntax (both forms are accepted to match the backend
// substituter in pkg/platform/prompts.go, which handles both via literal
// strings.ReplaceAll):
//
//   {{name}}     — preferred
//   {name}       — legacy
//
// `name` must start with a letter or underscore and may contain letters,
// digits, or underscores. Whitespace inside the braces is NOT tolerated
// because the backend substituter is a literal string replace ({{ name }}
// would survive substitution at runtime).
export function extractPromptArguments(
  content: string,
  existing: Prompt["arguments"] | undefined,
): Prompt["arguments"] {
  // One pass over the content, matching either form. `{{` is tried before
  // `{` so that "{{foo}}" is captured as a single double-brace placeholder
  // rather than as "{" + "{foo}" + "}".
  const re = /\{\{([a-zA-Z_][a-zA-Z0-9_]*)\}\}|\{([a-zA-Z_][a-zA-Z0-9_]*)\}/g;
  const seen = new Set<string>();
  const out: Prompt["arguments"] = [];
  const byName = new Map(existing?.map((a) => [a.name, a]) ?? []);
  let m: RegExpExecArray | null;
  while ((m = re.exec(content)) !== null) {
    const name = (m[1] ?? m[2])!;
    if (seen.has(name)) continue;
    seen.add(name);
    const prior = byName.get(name);
    out.push(prior ?? { name, description: "", required: true });
  }
  return out;
}
