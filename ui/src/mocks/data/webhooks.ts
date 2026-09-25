import type { WebhookSource, WebhookStatus } from "@/api/admin/types";

// Webhook sources for the demo portal and the page tests (#1870): one busy
// HMAC source with rejections, one quiet header-token source that is disabled.

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
];

export const mockWebhookStatus: Record<string, WebhookStatus> = {
  "email-events": {
    last_hour: { accepted: 18342, unauthorized: 3 },
    last_day: { accepted: 402113, unauthorized: 12, buffer_full: 40 },
    last_segment_at: hoursAgo(0.001),
    last_compacted_window: hourStart(1),
    pending: 1,
    failing: 0,
    oldest_window: hourStart(24 * 30),
    rejections: [
      { at: hoursAgo(0.2), outcome: "unauthorized", reason: "the timestamp is outside the tolerance window" },
      { at: hoursAgo(3), outcome: "buffer_full", reason: "the source's buffer is full" },
      { at: hoursAgo(5), outcome: "unauthorized", reason: "the signature does not match" },
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
    rejections: [],
  },
};
