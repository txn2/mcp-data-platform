import { Globe, Users, User } from "lucide-react";
import type { UserProfile } from "@/stores/auth";

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
