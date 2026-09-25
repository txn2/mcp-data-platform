// Inbound webhook sources (#1870), as /api/v1/admin/webhooks/sources returns
// them. A view never carries a secret: `secret_set` says whether one is
// stored, and `previous_secret_until` how long the one before a rotation is
// still accepted.

export type WebhookAuthMode = "hmac" | "header_token" | "basic" | "path_token";

export interface WebhookAuthView {
  mode: WebhookAuthMode;
  secret_set: boolean;
  previous_secret_until?: string;
  algorithm?: string;
  signature_header?: string;
  encoding?: string;
  prefix?: string;
  timestamp_header?: string;
  tolerance_seconds?: number;
  signed?: string;
  header?: string;
  username?: string;
}

export interface WebhookConfig {
  handshake?: "none" | "cloudevents";
  max_body_bytes?: number;
  split?: string;
  event_id_path?: string;
  event_type_path?: string;
  key_path?: string;
  persona?: string;
  /** The length of the window events are partitioned and compacted by. */
  compact_every_minutes?: number;
  flush_max_events?: number;
  flush_max_interval_ms?: number;
  flush_max_bytes?: number;
  buffer_limit?: number;
  rate_limit_per_minute?: number;
  rate_limit_burst?: number;
  raw_retention_days?: number;
  compacted_retention_days?: number;
}

export interface WebhookSource {
  name: string;
  enabled: boolean;
  connection: string;
  /** Where the sender posts, relative to the platform's address. */
  path: string;
  /** What readers query, in the connection's scratch schema. */
  table: string;
  auth: WebhookAuthView;
  config: WebhookConfig;
  created_by?: string;
  created_at: string;
  updated_at: string;
}

export interface WebhookRejection {
  at: string;
  outcome: string;
  reason: string;
}

export interface WebhookStatus {
  last_hour: Record<string, number>;
  last_day: Record<string, number>;
  last_segment_at: string | null;
  last_compacted_window: string | null;
  pending: number;
  failing: number;
  last_error?: string;
  oldest_window: string | null;
  rejections: WebhookRejection[];
}

export interface WebhookSourceDetail {
  source: WebhookSource;
  status: WebhookStatus;
}

export interface WebhookAuthInput {
  mode: WebhookAuthMode;
  /** Write-only. Empty on an update keeps the stored secret. */
  secret?: string;
  algorithm?: string;
  signature_header?: string;
  encoding?: string;
  prefix?: string;
  timestamp_header?: string;
  tolerance_seconds?: number;
  signed?: string;
  header?: string;
  username?: string;
}

export interface WebhookSourceInput {
  name?: string;
  enabled?: boolean;
  connection?: string;
  auth: WebhookAuthInput;
  config: WebhookConfig;
  rotation_overlap_seconds?: number;
}
