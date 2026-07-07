import { Globe, Users, User } from "lucide-react";

export const CATEGORIES = ["samples", "playbooks", "templates", "references"] as const;

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
