import type { ThreadWithMeta, ThreadEvent } from "@/api/portal/types";

const ME = "sarah.chen@example.com";
const SME = "dana.sme@example.com";

// Seeded feedback threads. ast-001 / col-001 match the assets/collections mock
// data so the per-object panels and list badges render in dev + screenshots.
export const mockThreads: ThreadWithMeta[] = [
  {
    id: "thr-asset-1",
    kind: "correction",
    target_type: "asset",
    asset_id: "ast-001",
    anchor: { type: "text_quote", exact: "monthly active users", prefix: "the ", suffix: " metric" },
    target_version: 2,
    title: "We don't use that term",
    author_id: SME,
    author_email: SME,
    status: "open",
    requires_resolution: true,
    validation_state: "none",
    created_at: "2026-06-08T15:04:00Z",
    updated_at: "2026-06-09T09:12:00Z",
    event_count: 3,
    last_event_at: "2026-06-09T09:12:00Z",
    last_event_type: "comment",
  },
  {
    id: "thr-asset-2",
    kind: "question",
    target_type: "asset",
    asset_id: "ast-001",
    title: "Where does the revenue column come from?",
    author_id: ME,
    author_email: ME,
    status: "answered",
    requires_resolution: false,
    validation_state: "none",
    created_at: "2026-06-07T11:00:00Z",
    updated_at: "2026-06-07T16:30:00Z",
    event_count: 2,
    last_event_at: "2026-06-07T16:30:00Z",
    last_event_type: "comment",
  },
  {
    id: "thr-coll-1",
    kind: "suggestion",
    target_type: "collection",
    collection_id: "col-001",
    title: "Add a glossary section",
    author_id: SME,
    author_email: SME,
    status: "open",
    requires_resolution: false,
    validation_state: "none",
    created_at: "2026-06-06T10:00:00Z",
    updated_at: "2026-06-06T10:00:00Z",
    event_count: 1,
    last_event_at: "2026-06-06T10:00:00Z",
    last_event_type: "comment",
  },
  {
    id: "thr-standalone-1",
    kind: "comment",
    target_type: "standalone",
    title: "Quarterly data refresh is one day late",
    author_id: SME,
    author_email: SME,
    status: "open",
    requires_resolution: false,
    validation_state: "none",
    created_at: "2026-06-05T08:30:00Z",
    updated_at: "2026-06-05T08:30:00Z",
    event_count: 1,
    last_event_at: "2026-06-05T08:30:00Z",
    last_event_type: "comment",
  },
];

export const mockThreadEvents: Record<string, ThreadEvent[]> = {
  "thr-asset-1": [
    { id: "evt-a1-1", thread_id: "thr-asset-1", event_type: "comment", author_id: SME, author_email: SME, body: "We call these 'active practitioners', not 'monthly active users'.", created_at: "2026-06-08T15:04:00Z" },
    { id: "evt-a1-2", thread_id: "thr-asset-1", event_type: "comment", author_id: ME, author_email: ME, body: "Good catch, updating the dashboard copy.", created_at: "2026-06-08T17:20:00Z" },
    { id: "evt-a1-3", thread_id: "thr-asset-1", event_type: "comment", author_id: SME, author_email: SME, body: "Thanks — section 2 still has the old term.", created_at: "2026-06-09T09:12:00Z" },
  ],
  "thr-asset-2": [
    { id: "evt-a2-1", thread_id: "thr-asset-2", event_type: "comment", author_id: ME, author_email: ME, body: "Which source feeds the revenue column?", created_at: "2026-06-07T11:00:00Z" },
    { id: "evt-a2-2", thread_id: "thr-asset-2", event_type: "comment", author_id: SME, author_email: SME, body: "It's the finance mart, refreshed nightly.", created_at: "2026-06-07T16:30:00Z" },
  ],
  "thr-coll-1": [
    { id: "evt-c1-1", thread_id: "thr-coll-1", event_type: "comment", author_id: SME, author_email: SME, body: "A glossary up front would help new readers.", created_at: "2026-06-06T10:00:00Z" },
  ],
  "thr-standalone-1": [
    { id: "evt-s1-1", thread_id: "thr-standalone-1", event_type: "comment", author_id: SME, author_email: SME, body: "The Monday refresh landed Tuesday again this week.", created_at: "2026-06-05T08:30:00Z" },
  ],
};
