import type { WebhookHealth, WebhookStatusOverview } from "@/api/admin/types";

// What the webhooks overview (#1979) shows, as functions of the status
// response: the labels of health and outcomes, the split of a source's
// requests into accepted and rejected, and the volume series filled out to
// one bar per bucket.

/** OUTCOMES orders the request outcomes the platform counts, accepted first. */
export const OUTCOMES: { key: string; label: string }[] = [
  { key: "accepted", label: "Accepted" },
  { key: "unauthorized", label: "Unauthorized" },
  { key: "too_large", label: "Too large" },
  { key: "rate_limited", label: "Rate limited" },
  { key: "buffer_full", label: "Buffer full" },
  { key: "write_failed", label: "Write failed" },
  { key: "invalid_body", label: "Invalid body" },
];

/** outcomeLabel names an outcome; one the list does not know is shown with
 * its underscores as spaces, first letter capitalized. */
export function outcomeLabel(key: string): string {
  const known = OUTCOMES.find((o) => o.key === key);
  if (known) return known.label;
  const words = key.replace(/_/g, " ");
  return words.charAt(0).toUpperCase() + words.slice(1);
}

export const HEALTH_LABELS: Record<WebhookHealth, string> = {
  receiving: "Receiving",
  silent: "Silent",
  failing: "Failing",
  disabled: "Disabled",
};

export const HEALTH_VARIANTS: Record<WebhookHealth, "success" | "warning" | "danger" | "muted"> = {
  receiving: "success",
  silent: "warning",
  failing: "danger",
  disabled: "muted",
};

/** HEALTH_HINTS say why a source is in its state. */
export const HEALTH_HINTS: Record<WebhookHealth, string> = {
  receiving: "Received an event in the last 24 hours and has no window failing to compact.",
  silent: "Enabled, and no event received in the last 24 hours. A sender that stops produces no error here.",
  failing: "One or more windows failed to compact and will be tried again.",
  disabled: "Turned off: requests are answered 404 and nothing is stored.",
};

/** splitCounts sums a count map into accepted requests and every other
 * outcome, which is a rejection. */
export function splitCounts(counts: Record<string, number>): { accepted: number; rejected: number } {
  let accepted = 0;
  let rejected = 0;
  for (const [outcome, n] of Object.entries(counts)) {
    if (outcome === "accepted") accepted += n;
    else rejected += n;
  }
  return { accepted, rejected };
}

/** VolumeBucket is one bar: its start, and a count per outcome. */
export type VolumeBucket = { at: string } & Record<string, number | string>;

/** volumeSeries is the requests of one source, or of every source when
 * source is empty, as one bucket per bucket_seconds from the bucket holding
 * `from` to the one holding `generated_at`, with every outcome seen zero where
 * it had none. Outcomes are ordered as OUTCOMES, unknown ones after. */
export function volumeSeries(
  ov: WebhookStatusOverview,
  source: string,
): { buckets: VolumeBucket[]; outcomes: string[]; total: number } {
  const step = ov.bucket_seconds * 1000;
  const points = ov.volume.filter((p) => source === "" || p.source === source);
  const seen = new Set(points.map((p) => p.outcome));
  const outcomes = [
    ...OUTCOMES.map((o) => o.key).filter((k) => seen.has(k)),
    ...[...seen].filter((k) => !OUTCOMES.some((o) => o.key === k)).sort(),
  ];
  const byStart = new Map<number, VolumeBucket>();
  // Counts are kept per minute and read from the first whole minute at or
  // after `from`, so the first bar is the bucket holding that minute; the
  // bucket holding `from` itself would always be empty on the hour's range.
  const firstMinute = Math.ceil(new Date(ov.from).getTime() / 60000) * 60000;
  const first = Math.floor(firstMinute / step) * step;
  const last = Math.floor(new Date(ov.generated_at).getTime() / step) * step;
  if (step > 0) {
    for (let t = first; t <= last; t += step) {
      const bucket: VolumeBucket = { at: new Date(t).toISOString() };
      for (const o of outcomes) bucket[o] = 0;
      byStart.set(t, bucket);
    }
  }
  let total = 0;
  for (const p of points) {
    const t = new Date(p.at).getTime();
    let bucket = byStart.get(t);
    if (!bucket) {
      bucket = { at: new Date(t).toISOString() };
      for (const o of outcomes) bucket[o] = 0;
      byStart.set(t, bucket);
    }
    bucket[p.outcome] = (bucket[p.outcome] as number) + p.count;
    total += p.count;
  }
  const buckets = [...byStart.entries()].sort((a, b) => a[0] - b[0]).map(([, b]) => b);
  return { buckets, outcomes, total };
}
