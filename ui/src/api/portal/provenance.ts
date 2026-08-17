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
   */
  captures?: ProvenanceCapture[];
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
  kind: ProvenanceCallKind;
  tool: string;
  connection?: string;
  /** The query text, for a sql call. */
  statement?: string;
  /** The request line, for an api call. */
  method?: string;
  path?: string;
  operation_id?: string;
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
