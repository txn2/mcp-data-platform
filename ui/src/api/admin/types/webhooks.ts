// Inbound webhook sources (#1870), as /api/v1/admin/webhooks/sources returns
// them. A view never carries a secret: `secret_set` says whether one is
// stored, and `previous_secret_until` how long the one before a rotation is
// still accepted.

export type WebhookAuthMode = "hmac" | "header_token" | "basic" | "path_token" | "imap";

/** WebhookInput is where a source's events come from: posted to it, or read
 * from a mailbox the platform polls, each message one event (#2247). */
export type WebhookInput = "http" | "imap";

/** WebhookIMAP is the mailbox an imap source polls. The login is the source's
 * auth: mode imap, its username, and its password as the write-only secret. */
export interface WebhookIMAP {
  host: string;
  port?: number;
  tls?: "tls" | "starttls";
  folders: string[];
  /** Addresses and @domain entries whose mail becomes an event; anyone else's is counted and never stored. */
  allow_senders: string[];
  /** Absent is on: an allowed sender's mail is kept only with an aligned SPF, DKIM or DMARC pass. */
  require_auth_pass?: boolean;
  trusted_authserv_id?: string;
  tls_ca_bundle_pem?: string;
  poll_seconds?: number;
  max_message_bytes?: number;
  max_attachment_bytes?: number;
}

/** WebhookIMAPFolder is one folder of an imap source as its polls left it. */
export interface WebhookIMAPFolder {
  folder: string;
  uid_validity: number;
  last_uid: number;
  stored: number;
  hidden_not_allowed: number;
  hidden_unauthenticated: number;
  last_polled_at?: string;
  last_error?: string;
}

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
  id_header?: string;
  header_format?: string;
  header?: string;
  username?: string;
}

export interface WebhookConfig {
  /** Absent is http. */
  input?: WebhookInput;
  imap?: WebhookIMAP;
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
  /** How search indexes the compacted windows; absent leaves it to resources.index. */
  index?: "full" | "metadata" | "none";
}

export interface WebhookSource {
  name: string;
  enabled: boolean;
  connection: string;
  /** Where the sender posts, relative to the platform's address; empty for an imap source. */
  path: string;
  /** What readers query, in the connection's scratch schema. */
  table: string;
  auth: WebhookAuthView;
  config: WebhookConfig;
  created_by?: string;
  created_at: string;
  updated_at: string;
  /** What a create or change answered besides the source, without refusing
   * it (#2132, #2134); absent from a list or a read. */
  warnings?: string[];
}

/** WebhookRejection is a run of refused requests with one outcome and
 * reason: `count` of them, the first at `first_at`, the latest at `at`. */
export interface WebhookRejection {
  at: string;
  first_at: string;
  count: number;
  outcome: string;
  reason: string;
  /** SHA-256 of the refused request's body, when its events were read; empty otherwise. */
  request_hash: string;
  /** Ids of the refused request's first 20 events; empty when they were not read. */
  event_ids: string[];
}

/** WebhookSlowAck is the slowest delivery answered 202 in a span: when, how
 * long the sender waited, and how long its events spent in the buffer,
 * recording their window and writing their segment. */
export interface WebhookSlowAck {
  at: string;
  total_ms: number;
  buffer_ms: number;
  mark_ms: number;
  put_ms: number;
}

/** WebhookAckLatency is how long the deliveries answered 202 waited for that
 * answer. p50_ms and p99_ms are the upper bound of the bucket the percentile
 * falls in, capped at max_ms. */
export interface WebhookAckLatency {
  count: number;
  p50_ms: number;
  p99_ms: number;
  max_ms: number;
  slowest: WebhookSlowAck | null;
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
  ack_last_hour: WebhookAckLatency;
  ack_last_day: WebhookAckLatency;
  rejections: WebhookRejection[];
  /** Raw files recorded against the open window, and the 95th percentile of
   * that count over the windows of the last day (#2132). */
  segments_open_window: number;
  segments_per_window_p95: number;
}

export interface WebhookSourceDetail {
  source: WebhookSource;
  status: WebhookStatus;
  /** An imap source's folders; absent for an http source. */
  imap?: WebhookIMAPFolder[];
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
  id_header?: string;
  header_format?: string;
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

// The status of every source in one response (#1979), as
// /api/v1/admin/webhooks/status returns it.

/** WebhookHealth is a source's overall state. When more than one applies,
 * the first of disabled, failing, silent, receiving is reported. */
export type WebhookHealth = "receiving" | "silent" | "failing" | "disabled";

/** WebhookStatusRange is the span the series covers; custom takes a from and a to. */
export type WebhookStatusRange = "hour" | "day" | "week" | "custom";

/** WebhookCustomSpan is a custom range's bounds, RFC 3339. */
export interface WebhookCustomSpan {
  from: string;
  to: string;
}

export interface WebhookSourceStatus {
  name: string;
  enabled: boolean;
  health: WebhookHealth;
  auth_mode: WebhookAuthMode;
  connection: string;
  table: string;
  /** When the source last received an event; null when it never has. */
  last_event_at: string | null;
  last_hour: Record<string, number>;
  last_day: Record<string, number>;
  pending: number;
  failing: number;
  last_error?: string;
  ack_last_hour: WebhookAckLatency;
  ack_last_day: WebhookAckLatency;
  /** Raw files recorded against the open window, and the 95th percentile of
   * that count over the windows of the last day (#2132). */
  segments_open_window: number;
  segments_per_window_p95: number;
}

/** WebhookVolumePoint is one source's requests with one outcome in the
 * bucket starting at `at`. A bucket with no requests has no point. */
export interface WebhookVolumePoint {
  at: string;
  source: string;
  outcome: string;
  count: number;
}

export interface WebhookSourceRejection extends WebhookRejection {
  source: string;
}

export interface WebhookStatusOverview {
  generated_at: string;
  range: WebhookStatusRange;
  /** The series' lower bound: a minute is counted when it is at or after it. */
  from: string;
  /** Its upper bound, exclusive. */
  until: string;
  bucket_seconds: number;
  silent_after_seconds: number;
  sources: WebhookSourceStatus[];
  volume: WebhookVolumePoint[];
  rejections: WebhookSourceRejection[];
  /** Every receiver's local spool, by replica; empty where none spools (#2121). */
  spools: WebhookReplicaSpool[];
  /** The history over the range (#2150). */
  history: WebhookHistory;
}

/** WebhookStatusPoint counts one source's requests answered with one HTTP
 * status in one bucket; status 0 is a sender that left before its answer. */
export interface WebhookStatusPoint {
  at: string;
  source: string;
  http_status: number;
  count: number;
}

/** WebhookReplicaMinute is one receiver's bucket: events received and written
 * to object storage, and its spool's depth and oldest age. */
export interface WebhookReplicaMinute {
  at: string;
  replica: string;
  received_events: number;
  drained_events: number;
  drained_bytes: number;
  spool_bytes: number;
  spool_events: number;
  oldest_age_seconds: number;
}

/** WebhookAckPoint is one source's acknowledgement latency in one bucket,
 * with each stage's average; with a spool, put is the spool's sync. */
export interface WebhookAckPoint {
  at: string;
  source: string;
  count: number;
  p50_ms: number;
  p99_ms: number;
  max_ms: number;
  buffer_ms: number;
  mark_ms: number;
  put_ms: number;
}

/** WebhookCompactionRun is one compaction of one window. */
export interface WebhookCompactionRun {
  source: string;
  window_start: string;
  at: string;
  duration_ms: number;
  lag_seconds: number;
  segments: number;
  events: number;
  input_bytes: number;
  decoded_bytes: number;
  output_bytes: number;
}

export interface WebhookSourceStorage {
  source: string;
  raw_bytes: number;
  raw_segments: number;
  compacted_bytes: number;
  compacted_windows: number;
}

export interface WebhookHistory {
  statuses: WebhookStatusPoint[];
  replicas: WebhookReplicaMinute[];
  ack: WebhookAckPoint[];
  compactions: WebhookCompactionRun[];
  storage: WebhookSourceStorage[];
  sizing: {
    memory_limit_bytes: number;
    largest: {
      source: string;
      window_start: string;
      decoded_bytes: number;
      input_bytes: number;
      events: number;
      percent_of_limit: number;
    } | null;
    warn_at_percent: number;
    warning: boolean;
  };
}

/** WebhookThresholds are a warning and a critical value; 0 is off. */
export interface WebhookThresholds {
  warning: number;
  critical: number;
}

/** WebhookSourceRules are the per-source conditions; an absent rule takes the default. */
export interface WebhookSourceRules {
  refusal_minutes?: number;
  unauthorized_percent?: WebhookThresholds;
  ack_p99_ms?: WebhookThresholds;
  compaction_overdue_minutes?: WebhookThresholds;
  silent_after_minutes?: number;
}

/** WebhookAlertSettings are the webhook receiver's alerts (#2149). */
export interface WebhookAlertSettings {
  channels: string[];
  spool_fill_percent: WebhookThresholds;
  spool_age_percent: WebhookThresholds;
  drain_failures: WebhookThresholds;
  drain_failing_minutes: number;
  not_reporting_seconds: number;
  spool_volume_free_percent: WebhookThresholds;
  defaults: WebhookSourceRules;
  sources: Record<string, WebhookSourceRules>;
  updated_by?: string;
  updated_at?: string;
}

export type WebhookAlertInput = Partial<Omit<WebhookAlertSettings, "updated_by" | "updated_at">>;

/** One receiver's local spool as it last reported it (#2121). */
export interface WebhookReplicaSpool {
  replica: string;
  dir: string;
  bytes: number;
  events: number;
  files: number;
  /** When the oldest event waiting was received; null when the spool is empty. */
  oldest_at: string | null;
  max_bytes: number;
  max_age_seconds: number;
  drained_events: number;
  drain_failures: number;
  last_drain_at: string | null;
  last_error: string;
  started_at: string;
  updated_at: string;
  /** False for a receiver that has not reported for two minutes. */
  reporting: boolean;
}
