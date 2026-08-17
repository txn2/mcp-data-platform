// The call catalog (#1321). A call record is a recorded query or API
// invocation: what ran, what the caller said it was for, and what came of it.
// These shapes mirror internal/platform/callrecord.

/** Which kind of data access the call was. */
export type CallKind = "sql" | "api";

/**
 * What became of the call. Derived on every read from the call's own result,
 * from whatever later cited it, and from what the same session ran afterwards,
 * so it is never stale with respect to the asset or capture that gives it
 * meaning.
 */
export type CallOutcome = "failed" | "satisfied" | "superseded" | "ran";

/** How a record came to be satisfied. */
export type CallSatisfiedBy = "asset" | "export" | "capture";

/** One thing built from a call. */
export interface CallArtifact {
  kind: CallSatisfiedBy;
  id: string;
  name: string;
}

export interface CallRecord {
  id: string;
  event_id: string;
  reference: string;
  kind: CallKind;
  tool_name: string;
  connection?: string;
  statement?: string;
  method?: string;
  path?: string;
  operation_id?: string;
  targets: string[];
  purpose?: string;
  user_id?: string;
  user_email?: string;
  session_id?: string;
  persona?: string;
  success: boolean;
  error_message?: string;
  duration_ms: number;
  response_chars: number;
  outcome: CallOutcome;
  satisfied_by?: CallSatisfiedBy;
  artifacts?: CallArtifact[];
  reuse_count: number;
  promoted_urn?: string;
  promoted_at?: string;
  promoted_by?: string;
  rejected_at?: string;
  rejected_by?: string;
  rejection_note?: string;
  created_at: string;
}

export interface CallListResponse {
  data: CallRecord[];
  total: number;
  page: number;
  per_page: number;
}

/** The facets both call lists accept. The caller is never one of them. */
export interface CallListParams {
  page?: number;
  perPage?: number;
  kind?: CallKind | "";
  connection?: string;
  outcome?: CallOutcome | "";
  target?: string;
  sessionId?: string;
  q?: string;
  /** Only the records a reviewer can act on, most reused first. */
  promotable?: boolean;
  /** Operator surface only: narrow to one caller's records. */
  userId?: string;
}

/**
 * The wire name of each facet. Written as a table rather than a run of ifs so
 * the two surfaces cannot disagree about what a facet is called, and so adding
 * one is a line here.
 */
const FACET_PARAM: Record<string, string> = {
  page: "page",
  perPage: "per_page",
  kind: "kind",
  connection: "connection",
  outcome: "outcome",
  target: "target",
  sessionId: "session_id",
  q: "q",
  userId: "user_id",
};

/** Builds the query string both call list endpoints accept. */
export function callListQuery(params: CallListParams): string {
  const search = new URLSearchParams();
  for (const [key, param] of Object.entries(FACET_PARAM)) {
    const value = params[key as keyof CallListParams];
    if (value) search.set(param, String(value));
  }
  // The review queue is a named view rather than a boolean facet: the server
  // decides what "awaiting review" means, and the client only asks for it.
  if (params.promotable) search.set("queue", "promotable");
  const qs = search.toString();
  return qs ? `?${qs}` : "";
}
