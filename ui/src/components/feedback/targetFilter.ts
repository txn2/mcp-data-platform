import type { FeedbackTarget, Thread } from "@/api/portal/types";
import type { ThreadListFilter } from "@/api/portal/hooks";

// filterForTarget maps a feedback target to the thread-list query filter the
// backend expects (one object id, or target_type=standalone).
export function filterForTarget(target: FeedbackTarget): ThreadListFilter {
  switch (target.type) {
    case "asset":
      return { asset_id: target.id };
    case "collection":
      return { collection_id: target.id };
    case "prompt":
      return { prompt_id: target.id };
    case "knowledge_page":
      return { knowledge_page_id: target.id };
    case "standalone":
      return { target_type: "standalone" };
  }
}

// targetLabel is the human label shown in the panel header.
export function targetLabel(target: FeedbackTarget): string {
  switch (target.type) {
    case "asset":
      return "Asset feedback";
    case "collection":
      return "Collection feedback";
    case "prompt":
      return "Prompt feedback";
    case "knowledge_page":
      return "Knowledge page feedback";
    case "standalone":
      return "Feedback";
  }
}

// targetOfThread maps a stored thread back to its feedback target, so a view
// holding only the thread (the detail pane and its reply box) can ask target-
// scoped questions -- who may be mentioned on it, above all.
export function targetOfThread(thread: Thread): FeedbackTarget {
  switch (thread.target_type) {
    case "asset":
      return { type: "asset", id: thread.asset_id ?? "" };
    case "collection":
      return { type: "collection", id: thread.collection_id ?? "" };
    case "prompt":
      return { type: "prompt", id: thread.prompt_id ?? "" };
    case "knowledge_page":
      return { type: "knowledge_page", id: thread.knowledge_page_id ?? "" };
    default:
      return { type: "standalone" };
  }
}
