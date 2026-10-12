import type {
  WebhookAckLatency,
  WebhookHealth,
  WebhookSlowAck,
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
];

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
    rejections: [{ at: hoursAgo(0.5), first_at: hoursAgo(0.6), count: 5, outcome: "invalid_body", reason: "the body is not JSON", request_hash: "", event_ids: [] }],
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
function mockVolume(sources: WebhookSource[], range: WebhookStatusRange, from: number, step: number): WebhookVolumePoint[] {
  const buckets = Math.round((range === "hour" ? 3600_000 : 24 * 3600_000) / step);
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
): WebhookStatusOverview {
  const now = Date.now();
  const step = range === "hour" ? 60_000 : 15 * 60_000;
  const from = now - (range === "hour" ? 3600_000 : 24 * 3600_000);
  const sources = source ? all.filter((s) => s.name === source) : all;
  const ordered = [...sources].sort((a, b) => a.name.localeCompare(b.name));
  return {
    generated_at: new Date(now).toISOString(),
    range,
    from: new Date(from).toISOString(),
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
        ...(st.last_error ? { last_error: st.last_error } : {}),
      };
    }),
    volume: mockVolume(ordered, range, from, step),
    rejections: ordered
      .flatMap((s) => (mockWebhookStatus[s.name]?.rejections ?? []).map((r) => ({ source: s.name, ...r })))
      .filter((r) => new Date(r.at).getTime() >= from)
      .sort((a, b) => b.at.localeCompare(a.at))
      .slice(0, 50),
  };
}
