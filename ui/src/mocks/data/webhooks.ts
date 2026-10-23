import type {
  WebhookIMAPFolder,
  WebhookAckLatency,
  WebhookHealth,
  WebhookHistory,
  WebhookSlowAck,
  WebhookReplicaSpool,
  WebhookSource,
  WebhookStatus,
  WebhookStatusOverview,
  WebhookStatusRange,
  WebhookVolumePoint,
} from "@/api/admin/types";

// Webhook sources for the demo portal and the page tests (#1870, #1979): one
// busy HMAC source with rejections, one header-token source that is disabled,
// one basic-credentials source that has gone silent, and one path-token source
// whose windows are failing to compact.

const hoursAgo = (h: number) => new Date(Date.now() - h * 3600_000).toISOString();
const hourStart = (h: number) => {
  const d = new Date(Date.now() - h * 3600_000);
  d.setUTCMinutes(0, 0, 0);
  return d.toISOString();
};

export const mockWebhookSources: WebhookSource[] = [
  {
    name: "email-events",
    enabled: true,
    connection: "acme-scratch-resources",
    path: "/hooks/email-events",
    table: "webhook_email_events",
    auth: {
      mode: "hmac",
      secret_set: true,
      previous_secret_until: hoursAgo(-20),
      algorithm: "sha256",
      signature_header: "X-Signature",
      encoding: "hex",
      prefix: "sha256=",
      timestamp_header: "X-Timestamp",
      tolerance_seconds: 300,
      signed: "timestamp.body",
    },
    config: {
      handshake: "none",
      max_body_bytes: 1048576,
      split: "$",
      event_id_path: "$.sg_event_id",
      event_type_path: "$.event",
      key_path: "$.email",
      persona: "marketing",
      compact_every_minutes: 15,
      flush_max_events: 5000,
      flush_max_interval_ms: 1000,
      flush_max_bytes: 8388608,
      buffer_limit: 50000,
      raw_retention_days: 7,
      compacted_retention_days: 400,
    },
    created_by: "admin@example.com",
    created_at: hoursAgo(24 * 30),
    updated_at: hoursAgo(4),
  },
  {
    name: "crm-contacts",
    enabled: false,
    connection: "acme-scratch-resources",
    path: "/hooks/crm-contacts",
    table: "webhook_crm_contacts",
    auth: { mode: "header_token", secret_set: true, header: "X-Webhook-Token" },
    config: {
      handshake: "cloudevents",
      max_body_bytes: 1048576,
      event_id_path: "$.id",
      key_path: "$.contact.id",
      flush_max_events: 5000,
      flush_max_interval_ms: 1000,
      flush_max_bytes: 8388608,
      buffer_limit: 50000,
      rate_limit_per_minute: 600,
      raw_retention_days: 7,
      compacted_retention_days: 0,
    },
    created_by: "admin@example.com",
    created_at: hoursAgo(24 * 9),
    updated_at: hoursAgo(24 * 2),
  },
  {
    name: "billing-events",
    enabled: true,
    connection: "acme-scratch-resources",
    path: "/hooks/billing-events",
    table: "webhook_billing_events",
    auth: { mode: "basic", secret_set: true, username: "billing" },
    config: {
      handshake: "none",
      max_body_bytes: 1048576,
      event_id_path: "$.id",
      flush_max_events: 5000,
      flush_max_interval_ms: 1000,
      flush_max_bytes: 8388608,
      buffer_limit: 50000,
      raw_retention_days: 7,
      compacted_retention_days: 400,
    },
    created_by: "admin@example.com",
    created_at: hoursAgo(24 * 20),
    updated_at: hoursAgo(24 * 20),
  },
  {
    name: "order-updates",
    enabled: true,
    connection: "acme-scratch-resources",
    path: "/hooks/order-updates",
    table: "webhook_order_updates",
    auth: { mode: "path_token", secret_set: true },
    config: {
      handshake: "none",
      max_body_bytes: 1048576,
      event_id_path: "$.order.id",
      compact_every_minutes: 5,
      flush_max_events: 5000,
      flush_max_interval_ms: 1000,
      flush_max_bytes: 8388608,
      buffer_limit: 50000,
      raw_retention_days: 7,
      compacted_retention_days: 90,
    },
    created_by: "admin@example.com",
    created_at: hoursAgo(24 * 3),
    updated_at: hoursAgo(24 * 3),
  },
  {
    name: "meeting-summaries",
    enabled: true,
    connection: "acme-scratch-resources",
    path: "",
    table: "webhook_meeting_summaries",
    auth: { mode: "imap", secret_set: true, username: "platform@acme.example" },
    config: {
      input: "imap",
      imap: {
        host: "imap.acme.example",
        port: 993,
        tls: "tls",
        folders: ["INBOX"],
        allow_senders: ["no-reply@zoom.example"],
        require_auth_pass: true,
        trusted_authserv_id: "mx.acme.example",
        poll_seconds: 60,
        max_message_bytes: 10485760,
        max_attachment_bytes: 26214400,
      },
      persona: "analyst",
      compact_every_minutes: 60,
      raw_retention_days: 7,
      compacted_retention_days: 400,
    },
    created_by: "admin@example.com",
    created_at: hoursAgo(24 * 10),
    updated_at: hoursAgo(24 * 10),
  },
];

/** mockWebhookFolders are each imap source's folders as their last check left them. */
export const mockWebhookFolders: Record<string, WebhookIMAPFolder[]> = {
  "meeting-summaries": [
    {
      folder: "INBOX",
      uid_validity: 1730000000,
      last_uid: 46,
      stored: 41,
      hidden_not_allowed: 4,
      hidden_unauthenticated: 1,
      last_polled_at: hoursAgo(0.01),
    },
  ],
};

/** NO_ACKS is the latency of a source that accepted nothing. */
const NO_ACKS: WebhookAckLatency = { count: 0, p50_ms: 0, p99_ms: 0, max_ms: 0, slowest: null };

/** acks is a span's latency, its slowest request at hoursBack. */
function acks(count: number, p50: number, p99: number, slowest: Omit<WebhookSlowAck, "at">, hoursBack: number): WebhookAckLatency {
  return { count, p50_ms: p50, p99_ms: p99, max_ms: slowest.total_ms, slowest: { at: hoursAgo(hoursBack), ...slowest } };
}

export const mockWebhookStatus: Record<string, WebhookStatus> = {
  "email-events": {
    last_hour: { accepted: 18342, unauthorized: 3 },
    last_day: { accepted: 402113, unauthorized: 12, buffer_full: 40 },
    last_segment_at: hoursAgo(0.001),
    last_compacted_window: hourStart(1),
    pending: 1,
    failing: 0,
    oldest_window: hourStart(24 * 30),
    ack_last_hour: acks(18342, 1000, 2500, { total_ms: 2380, buffer_ms: 990, mark_ms: 1310, put_ms: 80 }, 0.3),
    ack_last_day: acks(402113, 1000, 2500, { total_ms: 24600, buffer_ms: 1000, mark_ms: 1600, put_ms: 22000 }, 13),
    segments_open_window: 412,
    segments_per_window_p95: 3650,
    rejections: [
      {
        at: hoursAgo(0.4),
        first_at: hoursAgo(0.4),
        count: 1,
        outcome: "sender_gone",
        reason: "the sender disconnected after 30s, before its events were written",
        request_hash: "9f2c4e7a1b3d5f60718293a4b5c6d7e8f9012a3b4c5d6e7f8091a2b3c4d5e6f7",
        event_ids: ["evt_01J9Z6Q4", "evt_01J9Z6Q5", "evt_01J9Z6Q6", "evt_01J9Z6Q7"],
      },
      { at: hoursAgo(0.1), first_at: hoursAgo(0.11), count: 42, outcome: "rate_limited", reason: "the source's rate limit was reached", request_hash: "", event_ids: [] },
      { at: hoursAgo(0.2), first_at: hoursAgo(0.2), count: 1, outcome: "unauthorized", reason: "the timestamp is outside the tolerance window", request_hash: "", event_ids: [] },
      { at: hoursAgo(3), first_at: hoursAgo(3), count: 1, outcome: "buffer_full", reason: "the source's buffer is full", request_hash: "", event_ids: [] },
      { at: hoursAgo(5), first_at: hoursAgo(5), count: 1, outcome: "unauthorized", reason: "the signature does not match", request_hash: "", event_ids: [] },
    ],
  },
  "crm-contacts": {
    last_hour: {},
    last_day: {},
    last_segment_at: hoursAgo(50),
    last_compacted_window: hourStart(49),
    pending: 0,
    failing: 0,
    oldest_window: hourStart(24 * 9),
    ack_last_hour: NO_ACKS,
    ack_last_day: NO_ACKS,
    segments_open_window: 0,
    segments_per_window_p95: 6,
    rejections: [],
  },
  "billing-events": {
    last_hour: {},
    last_day: { unauthorized: 2 },
    last_segment_at: hoursAgo(30),
    last_compacted_window: hourStart(30),
    pending: 0,
    failing: 0,
    oldest_window: hourStart(24 * 20),
    ack_last_hour: NO_ACKS,
    ack_last_day: NO_ACKS,
    segments_open_window: 0,
    segments_per_window_p95: 3,
    rejections: [{ at: hoursAgo(2), first_at: hoursAgo(2), count: 1, outcome: "unauthorized", reason: "the credentials do not match", request_hash: "", event_ids: [] }],
  },
  "order-updates": {
    last_hour: { accepted: 420, invalid_body: 5 },
    last_day: { accepted: 9800, invalid_body: 31 },
    last_segment_at: hoursAgo(0.01),
    last_compacted_window: hourStart(3),
    pending: 4,
    failing: 2,
    last_error: "segment webhooks/order-updates/dt=2026-09-29/hour=09/minute=05/r1-1.jsonl.gz: gzip: invalid header",
    oldest_window: hourStart(24 * 3),
    ack_last_hour: acks(420, 1000, 1000, { total_ms: 1040, buffer_ms: 1000, mark_ms: 12, put_ms: 28 }, 0.2),
    ack_last_day: acks(9800, 1000, 1000, { total_ms: 1210, buffer_ms: 1000, mark_ms: 160, put_ms: 50 }, 8),
    segments_open_window: 38,
    segments_per_window_p95: 120,
    rejections: [{ at: hoursAgo(0.5), first_at: hoursAgo(0.6), count: 5, outcome: "invalid_body", reason: "the body is not JSON", request_hash: "", event_ids: [] }],
  },
  "meeting-summaries": {
    last_hour: {},
    last_day: {},
    last_segment_at: hoursAgo(5),
    last_compacted_window: hourStart(6),
    pending: 1,
    failing: 0,
    oldest_window: hourStart(24 * 7),
    ack_last_hour: NO_ACKS,
    ack_last_day: NO_ACKS,
    segments_open_window: 0,
    segments_per_window_p95: 1,
    rejections: [],
  },
};

/** SILENT_AFTER is how long an enabled source may go without an event before
 * the platform reports it silent. */
const SILENT_AFTER_MS = 24 * 3600_000;

/** mockHealth is the platform's health rule: disabled, then failing, then
 * silent, then receiving. */
function mockHealth(source: WebhookSource, status: WebhookStatus): WebhookHealth {
  if (!source.enabled) return "disabled";
  if (status.failing > 0) return "failing";
  if (!status.last_segment_at || Date.now() - new Date(status.last_segment_at).getTime() > SILENT_AFTER_MS) return "silent";
  return "receiving";
}

/** mockVolume spreads each source's counts for the range over its buckets,
 * so the series sums to the counts the table shows. */
function mockVolume(sources: WebhookSource[], range: WebhookStatusRange, from: number, until: number, step: number): WebhookVolumePoint[] {
  const buckets = Math.max(1, Math.round((until - from) / step));
  const first = Math.ceil(from / step) * step;
  const out: WebhookVolumePoint[] = [];
  for (const s of sources) {
    const counts = (range === "hour" ? mockWebhookStatus[s.name]?.last_hour : mockWebhookStatus[s.name]?.last_day) ?? {};
    for (const [outcome, total] of Object.entries(counts)) {
      const each = Math.floor(total / buckets);
      for (let i = 0; i < buckets; i++) {
        const count = i === buckets - 1 ? total - each * (buckets - 1) : each;
        if (count > 0) out.push({ at: new Date(first + i * step).toISOString(), source: s.name, outcome, count });
      }
    }
  }
  return out.sort((a, b) => a.at.localeCompare(b.at));
}

/** mockWebhookOverview is what /api/v1/admin/webhooks/status returns for
 * these sources. */
export function mockWebhookOverview(
  all: WebhookSource[],
  range: WebhookStatusRange,
  source = "",
  custom?: { from: number; to: number },
): WebhookStatusOverview {
  const now = custom?.to ?? Date.now();
  const span = custom ? custom.to - custom.from : RANGE_SPAN_MS[range];
  const step = custom ? customStepMs(span) : RANGE_STEP_MS[range];
  const from = now - span;
  const sources = source ? all.filter((s) => s.name === source) : all;
  const ordered = [...sources].sort((a, b) => a.name.localeCompare(b.name));
  return {
    generated_at: new Date(now).toISOString(),
    range,
    from: new Date(from).toISOString(),
    until: new Date(now).toISOString(),
    bucket_seconds: step / 1000,
    silent_after_seconds: SILENT_AFTER_MS / 1000,
    sources: ordered.map((s) => {
      const st = mockWebhookStatus[s.name]!;
      return {
        name: s.name,
        enabled: s.enabled,
        health: mockHealth(s, st),
        auth_mode: s.auth.mode,
        connection: s.connection,
        table: s.table,
        last_event_at: st.last_segment_at,
        last_hour: st.last_hour,
        last_day: st.last_day,
        pending: st.pending,
        failing: st.failing,
        ack_last_hour: st.ack_last_hour,
        ack_last_day: st.ack_last_day,
        segments_open_window: st.segments_open_window,
        segments_per_window_p95: st.segments_per_window_p95,
        ...(st.last_error ? { last_error: st.last_error } : {}),
      };
    }),
    volume: mockVolume(ordered, range, from, now, step),
    rejections: ordered
      .flatMap((s) => (mockWebhookStatus[s.name]?.rejections ?? []).map((r) => ({ source: s.name, ...r })))
      .filter((r) => new Date(r.at).getTime() >= from)
      .sort((a, b) => b.at.localeCompare(a.at))
      .slice(0, 50),
    spools: mockSpools,
    history: mockWebhookHistory(ordered.map((s) => s.name), from, now, step),
  };
}

const RANGE_SPAN_MS: Record<WebhookStatusRange, number> = {
  hour: 3600_000,
  day: 24 * 3600_000,
  week: 7 * 24 * 3600_000,
  custom: 3600_000,
};

const RANGE_STEP_MS: Record<WebhookStatusRange, number> = {
  hour: 60_000,
  day: 15 * 60_000,
  week: 3600_000,
  custom: 60_000,
};

// customStepMs is the bucket a custom span is drawn in, as the server picks it.
function customStepMs(span: number): number {
  if (span <= 2 * 3600_000) return 60_000;
  if (span <= 36 * 3600_000) return 15 * 60_000;
  if (span <= 8 * 24 * 3600_000) return 3600_000;
  return 6 * 3600_000;
}

/** mockWebhookHistory is the #2150 telemetry over a span: a burst a third of
 * the way in that the second replica's spool absorbs while object storage is
 * slow, then drains; a run of 401s from a rotated secret; and one window large
 * enough to be warned about against a 2 GiB limit. */
export function mockWebhookHistory(sources: string[], from: number, until: number, step: number): WebhookHistory {
  const buckets: number[] = [];
  for (let t = Math.floor(from / step) * step; t < until; t += step) buckets.push(t);
  const n = buckets.length;
  const burst = (i: number) => (i >= Math.floor(n / 3) && i < Math.floor(n / 3) + Math.max(2, Math.floor(n / 8)) ? 6 : 1);
  const perStep = step / 60_000;
  const statuses: WebhookHistory["statuses"] = [];
  const replicas: WebhookHistory["replicas"] = [];
  const ack: WebhookHistory["ack"] = [];
  let backlog = 0;
  buckets.forEach((t, i) => {
    const at = new Date(t).toISOString();
    for (const source of sources) {
      const accepted = Math.round(30 * perStep * burst(i));
      statuses.push({ at, source, http_status: 202, count: accepted });
      if (i % 7 === 3) statuses.push({ at, source, http_status: 401, count: Math.round(2 * perStep) });
      if (burst(i) > 1) statuses.push({ at, source, http_status: 503, count: Math.round(perStep) });
      ack.push({
        at, source, count: accepted, p50_ms: burst(i) > 1 ? 250 : 25, p99_ms: burst(i) > 1 ? 2500 : 100,
        max_ms: burst(i) > 1 ? 5700 : 180, buffer_ms: 2, mark_ms: 4, put_ms: burst(i) > 1 ? 1400 : 12,
      });
    }
    const received = Math.round(30 * perStep * burst(i) * Math.max(1, sources.length));
    const drained = burst(i) > 1 ? Math.round(received / 3) : received + Math.min(backlog, received);
    backlog = Math.max(0, backlog + received - drained);
    replicas.push({
      at, replica: "mcp-data-platform-0", received_events: received, drained_events: received,
      drained_bytes: received * 640, spool_bytes: 2048, spool_events: 3, oldest_age_seconds: 1,
    });
    replicas.push({
      at, replica: "mcp-data-platform-1", received_events: received, drained_events: drained,
      drained_bytes: drained * 640, spool_bytes: backlog * 640, spool_events: backlog, oldest_age_seconds: backlog > 0 ? 900 : 1,
    });
  });
  const compactions: WebhookHistory["compactions"] = sources.flatMap((source, si) =>
    buckets
      .filter((_, i) => i % Math.max(1, Math.round(3600_000 / step)) === 0)
      .map((t, i) => ({
        source,
        window_start: new Date(t).toISOString(),
        at: new Date(t + 3600_000 + 150_000).toISOString(),
        duration_ms: 900 + i * 40,
        lag_seconds: 150 + (i % 4) * 60,
        segments: 120 + si * 40,
        events: 40_000,
        input_bytes: 9_600_000,
        decoded_bytes: si === 0 && i === 1 ? 1_210_000_000 : 64_000_000,
        output_bytes: 4_100_000,
      })),
  );
  const largest = [...compactions].sort((a, b) => b.decoded_bytes - a.decoded_bytes)[0];
  const limit = 2 * 1024 * 1024 * 1024;
  return {
    statuses,
    replicas,
    ack,
    compactions,
    storage: sources.map((source, i) => ({
      source, raw_bytes: 210_000_000 + i * 30_000_000, raw_segments: 2_800 + i * 100,
      compacted_bytes: 4_400_000_000 + i * 900_000_000, compacted_windows: 9_600,
    })),
    sizing: {
      memory_limit_bytes: limit,
      largest: largest
        ? {
            source: largest.source, window_start: largest.window_start, decoded_bytes: largest.decoded_bytes,
            input_bytes: largest.input_bytes, events: largest.events, percent_of_limit: (100 * largest.decoded_bytes) / limit,
          }
        : null,
      warn_at_percent: 50,
      warning: !!largest && (100 * largest.decoded_bytes) / limit >= 50,
    },
  };
}

/** mockSpools are two receivers' spools (#2121): one draining, one that
 * stopped with events waiting. */
const mockSpools: WebhookReplicaSpool[] = [
  {
    replica: "mcp-data-platform-0",
    dir: "/var/lib/mcp-data-platform/spool",
    bytes: 182_400,
    events: 212,
    files: 2,
    oldest_at: hoursAgo(0.001),
    max_bytes: 1 << 30,
    max_age_seconds: 3600,
    drained_events: 402_113,
    drain_failures: 0,
    last_drain_at: hoursAgo(0.0005),
    last_error: "",
    started_at: hoursAgo(30),
    updated_at: hoursAgo(0.002),
    reporting: true,
  },
  {
    replica: "mcp-data-platform-1",
    dir: "/var/lib/mcp-data-platform/spool",
    bytes: 9_830_400,
    events: 11_402,
    files: 7,
    oldest_at: hoursAgo(0.4),
    max_bytes: 1 << 30,
    max_age_seconds: 3600,
    drained_events: 380_220,
    drain_failures: 14,
    last_drain_at: hoursAgo(0.42),
    last_error: "writing segment: put webhooks/esp/raw/dt=...: context deadline exceeded",
    started_at: hoursAgo(30),
    updated_at: hoursAgo(0.35),
    reporting: false,
  },
];
