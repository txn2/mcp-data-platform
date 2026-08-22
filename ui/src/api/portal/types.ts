export interface AssetCollectionRef {
  id: string;
  name: string;
}

export interface Asset {
  id: string;
  owner_id: string;
  owner_email: string;
  name: string;
  // Optional: the API serializes description with `omitempty`, so an asset
  // with no description omits the field entirely (arrives as undefined).
  description?: string;
  content_type: string;
  s3_bucket: string;
  s3_key: string;
  thumbnail_s3_key?: string;
  // Dark-mode thumbnail variant. Only present for themeable content types
  // (markdown, CSV); other types reuse thumbnail_s3_key in both modes.
  thumbnail_dark_s3_key?: string;
  size_bytes: number;
  tags: string[];
  provenance: Provenance;
  session_id: string;
  current_version: number;
  // How many versions this asset keeps. Absent means the asset has no opinion
  // and inherits the deployment's portal.max_versions; 0 keeps every version;
  // N keeps the newest N. The API serializes it with `omitempty`, so an
  // inheriting asset omits the field entirely.
  max_versions?: number;
  collections?: AssetCollectionRef[];
  created_at: string;
  updated_at: string;
  deleted_at?: string;
}

export interface AssetVersion {
  id: string;
  asset_id: string;
  version: number;
  s3_key: string;
  s3_bucket: string;
  content_type: string;
  size_bytes: number;
  created_by: string;
  change_summary: string;
  created_at: string;
}

export type {
  Provenance,
  ProvenanceCapture,
  ProvenanceCall,
  ProvenanceCallKind,
  ProvenanceOutcome,
  ProvenanceToolCall,
} from "./provenance";
import type { Provenance } from "./provenance";

export type SharePermission = "viewer" | "editor";

/**
 * Who may open a share link. "restricted" admits only the named recipient (and
 * the sender), "authenticated" any signed-in user, "public" anyone with the
 * link. Shares default to restricted when they name a recipient and to
 * authenticated when they do not; "public" is only ever explicit.
 */
export type ShareAccessMode = "restricted" | "authenticated" | "public";

export interface Share {
  id: string;
  asset_id: string;
  token: string;
  created_by: string;
  shared_with_user_id?: string;
  shared_with_email?: string;
  permission: SharePermission;
  access_mode: ShareAccessMode;
  expires_at?: string;
  revoked: boolean;
  access_count: number;
  last_accessed_at?: string;
  created_at: string;
  hide_expiration?: boolean;
  notice_text?: string;
}

/**
 * CreateShareBody is the share-creation request body, identical for assets,
 * collections, and prompts because the server decodes all three with one
 * type. It lives here rather than being restated per hook so the three
 * cannot drift apart.
 *
 * `expires_in` belongs to public links alone: the server requires it for
 * `access_mode: "public"` and refuses it for every other share, because a
 * share that resolves against who the viewer is ends when it is revoked, not
 * on a clock.
 */
export interface CreateShareBody {
  expires_in?: string;
  shared_with_user_id?: string;
  shared_with_email?: string;
  hide_expiration?: boolean;
  notice_text?: string;
  permission?: SharePermission;
  access_mode?: ShareAccessMode;
  /** Omitted means notify; false shares without sending the recipient email. */
  notify?: boolean;
  /** Plain-text note from the sharer, delivered only in the notification email. */
  message?: string;
}

export interface SharedAsset {
  asset: Asset;
  share_id: string;
  shared_by: string;
  shared_at: string;
  permission: SharePermission;
}

export interface AssetResponse extends Asset {
  share_permission?: SharePermission;
  is_owner: boolean;
}

export interface ShareSummary {
  has_user_share: boolean;
  has_public_link: boolean;
}

export interface PaginatedResponse<T> {
  data: T[];
  total: number;
  limit: number;
  offset: number;
  share_summaries?: Record<string, ShareSummary>;
}

export interface ShareResponse {
  share: Share;
  share_url?: string;
}

// --- Collection types ---

export interface CollectionConfig {
  thumbnail_size?: "large" | "medium" | "small" | "none";
}

export interface Collection {
  id: string;
  owner_id: string;
  owner_email: string;
  name: string;
  description: string;
  thumbnail_s3_key?: string;
  config: CollectionConfig;
  sections: CollectionSection[];
  asset_tags?: string[];
  created_at: string;
  updated_at: string;
  deleted_at?: string;
}

export interface CollectionSection {
  id: string;
  collection_id: string;
  title: string;
  description: string;
  position: number;
  items: CollectionItem[];
  created_at: string;
}

export interface CollectionItem {
  id: string;
  section_id: string;
  asset_id: string;
  position: number;
  asset_name?: string;
  asset_content_type?: string;
  asset_thumbnail_s3_key?: string;
  asset_description?: string;
  created_at: string;
}

export interface CollectionResponse extends Collection {
  is_owner: boolean;
  share_permission?: SharePermission;
  /** May change the collection itself: name, description, settings, sections, thumbnail. */
  can_edit: boolean;
  /** Owner authority: delete, share, and read the share list. */
  can_manage: boolean;
}

export interface SharedCollection {
  collection: Collection;
  share_id: string;
  shared_by: string;
  shared_at: string;
  permission: SharePermission;
}

// Activity types (user-scoped audit metrics)
export interface ActivityOverview {
  total_calls: number;
  success_rate: number;
  avg_duration_ms: number;
  unique_users: number;
  unique_tools: number;
  enrichment_rate: number;
  error_count: number;
}

export interface TimeseriesBucket {
  bucket: string;
  count: number;
  success_count: number;
  error_count: number;
  avg_duration_ms: number;
}

export interface BreakdownEntry {
  dimension: string;
  count: number;
  success_rate: number;
  avg_duration_ms: number;
}

// Knowledge types (user-scoped insights)
export interface Insight {
  id: string;
  created_at: string;
  session_id: string;
  captured_by: string;
  persona: string;
  source: string;
  category: string;
  insight_text: string;
  confidence: string;
  entity_urns: string[];
  related_columns: RelatedColumn[];
  suggested_actions: SuggestedAction[];
  status: string;
  reviewed_by?: string;
  reviewed_at?: string;
  review_notes?: string;
  applied_by?: string;
  applied_at?: string;
  changeset_ref?: string;
}

export interface RelatedColumn {
  urn: string;
  column: string;
  relevance: string;
}

export interface SuggestedAction {
  action_type: string;
  target: string;
  detail: string;
  query_sql?: string;
  query_description?: string;
}

export interface InsightStats {
  total_pending: number;
  by_status: Record<string, number>;
  by_category: Record<string, number>;
  by_confidence: Record<string, number>;
}

// Memory types (user-scoped memory records)
export interface MemoryRecord {
  id: string;
  created_at: string;
  updated_at: string;
  created_by: string;
  persona: string;
  dimension: string;
  // sink_class is the #633 lifecycle axis (personal_preference, episodic_event,
  // business_knowledge, operational_rule, schema_entity). It is what the Memory
  // view classifies by; dimension is the legacy axis kept only for back-compat.
  sink_class?: string;
  content: string;
  category: string;
  confidence: string;
  source: string;
  entity_urns: string[];
  related_columns: RelatedColumn[];
  metadata: Record<string, unknown>;
  status: string;
  stale_reason?: string;
  stale_at?: string;
  last_verified?: string;
}

export interface MemoryStats {
  total: number;
  by_dimension: Record<string, number>;
  by_category: Record<string, number>;
  by_status: Record<string, number>;
}

// Search results carry a relevance score alongside the record fields.
export type ScoredMemoryRecord = MemoryRecord & { score: number };
export type ScoredInsight = Insight & { score: number };

// Asset and collection relevance-search results nest the entity under a key
// (matching the Go ScoredAsset / ScoredCollection payloads), mirroring
// ScoredPrompt rather than the flat memory/insight shape.
export interface ScoredAsset {
  asset: Asset;
  score: number;
}
export interface ScoredCollection {
  collection: Collection;
  score: number;
}

// --- Feedback thread types (#601) ---

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

// --- Known-users directory for the share picker (#614) ---

export interface DirectoryUser {
  email: string;
  first_name?: string;
  last_name?: string;
  confirmed: boolean;
}

export interface DirectoryUsersResponse {
  users: DirectoryUser[];
  total: number;
}

// --- Knowledge pages (#633) ---

export interface KnowledgePage {
  id: string;
  slug?: string;
  title: string;
  summary?: string;
  body: string;
  tags: string[];
  created_by?: string;
  created_email?: string;
  updated_by?: string;
  current_version: number;
  /** Shipped with the platform and reconciled at startup; read-only (deleting hides it). */
  builtin?: boolean;
  created_at: string;
  updated_at: string;
  deleted_at?: string;
}

export interface KnowledgePageVersion {
  id: string;
  page_id: string;
  version: number;
  title: string;
  summary?: string;
  body: string;
  tags: string[];
  created_by?: string;
  change_summary?: string;
  created_at: string;
}

export interface KnowledgePageListResponse {
  pages: KnowledgePage[];
  total: number;
}

export interface ScoredKnowledgePage {
  page: KnowledgePage;
  score: number;
}

export interface KnowledgePageVersionsResponse {
  versions: KnowledgePageVersion[];
  total: number;
}

/** Create/update payload for a knowledge page. */
export interface KnowledgePageInput {
  slug?: string;
  title: string;
  summary?: string;
  body?: string;
  tags?: string[];
  change_summary?: string;
  /**
   * Override the create-time duplicate gate (#705). When the content is highly
   * similar to an existing page, create is rejected with the candidates; set this
   * to create a separate page anyway. Ignored on update.
   */
  force_new?: boolean;
}

/** One near-duplicate page surfaced when the create gate blocks (#705). */
export interface KnowledgePageDedupCandidate {
  id: string;
  slug?: string;
  title: string;
  score: number;
}

/** 409 body the create endpoint returns when the dedup gate blocks a near-duplicate. */
export interface KnowledgePageDuplicateResponse {
  duplicate_blocked: boolean;
  candidates: KnowledgePageDedupCandidate[];
  message: string;
}

// --- Unified knowledge search (GET /api/v1/portal/search, #661) ---

// SearchHit is one navigational pointer returned by the unified search. The
// shape mirrors the MCP search tool's hit so the portal and agent surfaces agree.
export interface SearchHit {
  text: string;
  source: string;
  ref: string;
  score: number;
  status?: string;
  entity_urns?: string[];
  dimension?: string;
}

// SearchGroup is one source's slice of the balanced display set.
export interface SearchGroup {
  source: string;
  hits: SearchHit[];
}

// SearchCoverage reports, per source, how many records matched vs how many are
// shown, so breadth beyond the display set stays visible. withheld counts the
// matches the caller's persona hid because they belong to connections it is not
// granted (#1108); it separates "nothing matched" from "matches you may not see".
export interface SearchCoverage {
  source: string;
  matched: number;
  shown: number;
  withheld?: number;
}

// SearchResponse is the GET /api/v1/portal/search envelope. withheld_notice is
// the one-line explanation of the coverage withheld counts (how many, why, and
// how to get access); it is present only when something was withheld.
export interface SearchResponse {
  groups: SearchGroup[];
  coverage: SearchCoverage[];
  count: number;
  ranking: string;
  withheld_notice?: string;
}
