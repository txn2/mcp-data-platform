/**
 * Feedback thread types (#601): the threads people leave on an asset, a
 * collection, a prompt or a knowledge page, the events on them, and the
 * aggregates the list pages read.
 *
 * They live apart from api/portal/types for the reason api/portal/provenance
 * does: types.ts is a barrel over every portal domain and is held to a line
 * budget, and this is the largest block in it that nothing else in the file
 * refers to.
 */

export type ThreadKind =
  | "comment"
  | "question"
  | "correction"
  | "rating"
  | "approval"
  | "rejection"
  | "suggestion";

export type ThreadTargetType =
  | "asset"
  | "collection"
  | "prompt"
  | "knowledge_page"
  | "standalone";

export type ThreadStatus =
  | "open"
  | "answered"
  | "resolved"
  | "wont_fix"
  | "acknowledged";

export type ThreadEventType =
  | "comment"
  | "status_change"
  | "resolution"
  | "rating"
  | "approval"
  | "rejection"
  | "validation_request"
  | "validation_result"
  | "insight_linked"
  | "changeset_linked";

export type ValidationState = "none" | "pending" | "validated" | "disputed";

// A W3C-style text-quote anchor (markdown/plaintext) or a collection section
// anchor. null means the thread is object-level (the whole target).
export interface TextQuoteAnchor {
  type: "text_quote";
  exact: string;
  prefix?: string;
  suffix?: string;
}
export interface SectionAnchor {
  type: "section";
  section_id: string;
}
export type ThreadAnchor = TextQuoteAnchor | SectionAnchor;

export interface Thread {
  id: string;
  kind: ThreadKind;
  target_type: ThreadTargetType;
  asset_id?: string;
  collection_id?: string;
  prompt_id?: string;
  knowledge_page_id?: string;
  anchor?: ThreadAnchor;
  target_version?: number;
  title?: string;
  author_id: string;
  author_email: string;
  status: ThreadStatus;
  requires_resolution: boolean;
  validation_state: ValidationState;
  insight_id?: string;
  created_at: string;
  updated_at: string;
  deleted_at?: string;
}

// A thread list row enriched with timeline aggregates.
export interface ThreadWithMeta extends Thread {
  event_count: number;
  last_event_at: string;
  last_event_type?: ThreadEventType;
}

// An activity-feed row (#617): a thread enriched with the display label of the
// asset, collection, or prompt it lives on, so the feed can link back to the
// item without a per-row lookup.
export interface ThreadActivityItem extends ThreadWithMeta {
  target_label: string;
}

export interface ThreadEvent {
  id: string;
  thread_id: string;
  event_type: ThreadEventType;
  author_id: string;
  author_email: string;
  body?: string;
  rating?: number;
  parent_event_id?: string;
  metadata?: Record<string, unknown>;
  created_at: string;
}

// Open-thread counts keyed by target id (for list-page badges).
export type ThreadCounts = Record<string, number>;

// Sign-off aggregation for an artifact (#603): N signed off of M stakeholders.
export interface SignoffSummary {
  signed_off: number;
  stakeholders: number;
}

// A changeset in a thread's resolved knowledge chain (#602).
export interface ThreadChainChangeset {
  id: string;
  target_urn: string;
  change_type: string;
  created_at: string;
  rolled_back: boolean;
}

// The resolved thread -> insight -> changeset chain returned by
// GET /portal/threads/{id}/chain. insight_id is empty until the thread is
// linked to a captured insight; changesets are the applied knowledge sourced
// from that insight.
export interface ThreadChain {
  thread_id: string;
  insight_id?: string;
  changesets: ThreadChainChangeset[];
}

// The discriminated target a feedback panel operates on. Mirrors ShareDialog's
// target union so one panel serves asset/collection/prompt and the standalone
// channel.
export type FeedbackTarget =
  | { type: "asset"; id: string; version?: number }
  | { type: "collection"; id: string }
  | { type: "prompt"; id: string }
  | { type: "knowledge_page"; id: string }
  | { type: "standalone" };
