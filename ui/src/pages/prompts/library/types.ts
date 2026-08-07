import type { PromptCollection } from "@/api/admin/types";
import type { Row } from "../promptList";

// The library's two buckets (#1010, #1124): My Prompts is every prompt the
// caller owns, at any scope (shared scopes carry a badge), plus prompts shared
// with them (attributed); Library is the approved shared set visible to them,
// grouped by collection.
export type Tab = "mine" | "library";

// LibraryGroup is one collection's slice of the Library bucket. An absent
// collection is the trailing default group.
export interface LibraryGroup {
  collection: PromptCollection | undefined;
  rows: Row[];
}
