import type { Prompt } from "@/api/admin/types";

// Sorting for the admin prompt table. The admin view sorts on the columns it
// alone shows (scope, owner, enabled), which is why it does not share the
// library's sort model in promptList.ts.

export type SortKey = "name" | "scope" | "description" | "owner" | "status";
export type SortDir = "asc" | "desc";

export const columns: { key: SortKey; label: string; width?: string }[] = [
  { key: "name", label: "Name" },
  { key: "scope", label: "Scope", width: "w-[100px]" },
  { key: "description", label: "Description" },
  { key: "owner", label: "Owner", width: "w-[160px]" },
  { key: "status", label: "Status", width: "w-[70px]" },
];

// The comparable key each column sorts on. "status" is the enabled flag, which
// has no natural string order, so it sorts as a two-valued key that puts active
// prompts first ascending.
const sortValues: Record<SortKey, (p: Prompt) => string> = {
  name: (p) => (p.display_name || p.name || "").toLowerCase(),
  scope: (p) => p.scope || "",
  description: (p) => (p.description || "").toLowerCase(),
  owner: (p) => (p.owner_email || "").toLowerCase(),
  status: (p) => (p.enabled ? "a" : "z"),
};

export function sortPrompts(prompts: Prompt[], key: SortKey, dir: SortDir): Prompt[] {
  const value = sortValues[key];
  const list = [...prompts];
  list.sort((a, b) => {
    const cmp = value(a).localeCompare(value(b));
    return dir === "asc" ? cmp : -cmp;
  });
  return list;
}
