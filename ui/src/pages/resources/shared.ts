import { Globe, Users, User } from "lucide-react";
import type { UserProfile } from "@/stores/auth";

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

/**
 * scopeLabel names the library a resource is filed in, from the point of view of
 * the person reading it.
 *
 * The viewer is a parameter because a user library is only "My Resources" to the
 * person it belongs to. The administrator's section lists every library at once,
 * and it named another person's library after the reader -- which the move
 * (#1502) turns from a display quirk into a false report of where a file was
 * just put. A library keyed by address is named by that address; one keyed by a
 * subject identifier is described, because a raw UUID names nobody.
 *
 * A viewer that is absent (nobody signed in yet) leaves a user library
 * undecided, which is the only honest answer without an identity to compare to.
 */
export function scopeLabel(scope: string, scopeId: string, viewer?: UserProfile | null) {
  if (scope === "global") return "Global";
  if (scope === "persona") return scopeId;
  if (viewer && scopeId !== "" && scopeId === viewer.user_id) return "My Resources";
  if (scopeId.includes("@")) return `${scopeId}'s library`;
  return viewer ? "Another person's library" : "My Resources";
}
