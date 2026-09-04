/**
 * What an asset was built from (#1320).
 *
 * The platform records an asset's sources by reference: each write captures
 * the audit event ids of the calls that fed it, alongside a snapshot of those
 * calls taken at write time. Audit rows are retained for a fixed window and
 * assets are not, so the snapshot is what keeps an old asset able to say what
 * produced it.
 */

export interface Provenance {
  /**
   * One entry per time the asset was written, each naming the calls that fed
   * that write. This is the live shape (#1320).
   *
   * A single asset read carries only the newest of them, oldest of those
   * first, because nothing bounds how many an asset refreshed on a schedule
   * accumulates (#1623). `captures_total` says how many the asset holds, and
   * the rest are read a page at a time.
   */
  captures?: ProvenanceCapture[];
  /**
   * How many captures the asset holds, present only when `captures` carries
   * fewer than that.
   */
  captures_total?: number;
  /** What assets saved before #1320 carry. Nothing writes it any more. */
  tool_calls?: ProvenanceToolCall[];
  session_id?: string;
  user_id?: string;
}

export interface ProvenanceCapture {
  /** The tool that took the capture: save_asset, manage_asset, *_export. */
  tool: string;
  captured_at: string;
  /** The asset version this capture produced, when known. */
  version?: number;
  session_id?: string;
  /** Audit event ids of the captured calls, in call order. */
  event_ids?: string[];
  /** The caller named these sources rather than the platform inferring them. */
  explicit?: boolean;
  /** More calls were eligible than the capture holds. */
  truncated?: boolean;
  calls?: ProvenanceCall[];
}

/** A query, an API invocation, or any other data call the platform served. */
export type ProvenanceCallKind = "sql" | "api" | "tool";

export type ProvenanceOutcome = "success" | "error";

export interface ProvenanceCall {
  event_id?: string;
  /**
   * The write NAMED this call as a source, rather than the platform sweeping it
   * up in the session's default window. A caller's `sources` argument names
   * calls, and so does the capturing call's own record of itself. It is what
   * makes the call's catalog record read `satisfied` (#1353).
   */
  cited?: boolean;
  kind: ProvenanceCallKind;
  tool: string;
  connection?: string;
  /** The query text, for a sql call. */
  statement?: string;
  /** The request line, for an api call. */
  method?: string;
  path?: string;
  operation_id?: string;
  /**
   * What an api call asked for: the path it addressed with the values it
   * passed substituted in, the query string it sent, and its request body on
   * the line below. It is what tells two calls to the same operation apart
   * (#1423). Absent on captures taken before it was recorded.
   */
  request?: string;
  /** What a call of any other kind addressed. */
  summary?: string;
  /** The reason the caller stated for the call (#1317). */
  purpose?: string;
  outcome: ProvenanceOutcome;
  error?: string;
  duration_ms?: number;
  timestamp: string;
}

export interface ProvenanceToolCall {
  tool_name: string;
  timestamp: string;
  parameters?: Record<string, unknown>;
}

/**
 * What a listing row says about an asset's provenance (#1623). A listing never
 * carries the captures themselves: they grow by one per write, and a library of
 * fifty assets carrying them was a megabyte of JSON.
 */
export interface ProvenanceSummary {
  captures: number;
  calls: number;
  first_captured_at?: string;
  last_captured_at?: string;
  last_tool?: string;
  last_session_id?: string;
}
