import { Globe, Users, User } from "lucide-react";

export const CATEGORIES = [
  "data",
  "visual",
  "samples",
  "playbooks",
  "templates",
  "references",
] as const;

/**
 * What each built-in category is for, shown while choosing one so the library
 * stays sorted by how the agent is meant to use the file rather than by what
 * kind of file it happens to be. A custom category has no hint.
 *
 * `data` and `visual` are the two that do not describe prose. Without them a
 * stored dataset and a logo both had to be filed under a name that means
 * "a document to read", and the hint shown while choosing said so.
 */
export const CATEGORY_HINTS: Record<string, string> = {
  data: "Records the agent reads as facts, not as examples: rosters, mappings, rate tables.",
  visual: "Logos, photographs, diagrams, and design elements meant to be displayed.",
  samples: "Example payloads and extracts the agent can pattern-match against.",
  playbooks: "Step-by-step procedures the agent should follow, not summarize.",
  templates: "Layouts a deliverable must be produced in, used verbatim.",
  references: "Data dictionaries, standards, and background documents to consult.",
};

export function scopeIcon(scope: string) {
  if (scope === "global") return Globe;
  if (scope === "persona") return Users;
  return User;
}

export function scopeLabel(scope: string, scopeId: string) {
  if (scope === "global") return "Global";
  if (scope === "persona") return scopeId;
  return "My Resources";
}
