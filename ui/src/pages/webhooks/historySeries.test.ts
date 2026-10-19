import { describe, expect, it } from "vitest";
import type { WebhookStatusOverview } from "@/api/admin/types";
import {
  ackSeries,
  bucketLabel,
  bucketTimes,
  compactionSeries,
  flowSeries,
  replicaNames,
  spoolSeries,
  statusLabel,
  statusSeries,
} from "./historySeries";

const at = (min: number) => new Date(Date.UTC(2026, 9, 17, 12, min)).toISOString();

function overview(history: Partial<WebhookStatusOverview["history"]>): WebhookStatusOverview {
  return {
    generated_at: at(3),
    range: "hour",
    from: at(0),
    until: at(3),
    bucket_seconds: 60,
    silent_after_seconds: 86400,
    sources: [],
    volume: [],
    rejections: [],
    spools: [],
    history: {
      statuses: [], replicas: [], ack: [], compactions: [], storage: [],
      sizing: { memory_limit_bytes: 0, largest: null, warn_at_percent: 50, warning: false },
      ...history,
    },
  };
}

describe("history series (#2150)", () => {
  it("has every bucket of the range, so a quiet minute is zero, not a gap", () => {
    expect(bucketTimes(overview({}))).toEqual([at(0), at(1), at(2)]);
    expect(bucketTimes({ ...overview({}), bucket_seconds: 0 })).toEqual([]);
  });

  it("counts requests by status for one source or all, naming no answer", () => {
    const data = overview({
      statuses: [
        { at: at(1), source: "esp", http_status: 202, count: 9 },
        { at: at(1), source: "crm", http_status: 202, count: 1 },
        { at: at(2), source: "esp", http_status: 0, count: 2 },
      ],
    });
    const all = statusSeries(data, "");
    expect(all.statuses).toEqual(["202", "No answer"]);
    expect(all.rows.find((r) => r.at === at(1))?.["202"]).toBe(10);
    expect(all.rows.find((r) => r.at === at(0))?.["202"]).toBe(0);
    expect(statusSeries(data, "crm").rows.find((r) => r.at === at(1))?.["202"]).toBe(1);
    expect(statusLabel(503)).toBe("503");
  });

  it("sets each receiver's received against written, and keeps its spool's deepest", () => {
    const data = overview({
      replicas: [
        { at: at(1), replica: "b", received_events: 30, drained_events: 10, drained_bytes: 1, spool_bytes: 9, spool_events: 20, oldest_age_seconds: 60 },
        { at: at(1), replica: "a", received_events: 5, drained_events: 5, drained_bytes: 1, spool_bytes: 0, spool_events: 0, oldest_age_seconds: 0 },
      ],
    });
    expect(replicaNames(data.history)).toEqual(["a", "b"]);
    const flow = flowSeries(data).find((r) => r.at === at(1))!;
    expect(flow["b received"]).toBe(30);
    expect(flow["b written"]).toBe(10);
    const spool = spoolSeries(data).find((r) => r.at === at(1))!;
    expect(spool["b events"]).toBe(20);
    expect(spool["b oldest"]).toBe(60);
  });

  it("weights each stage's average by the deliveries of each source", () => {
    const data = overview({
      ack: [
        { at: at(1), source: "esp", count: 3, p50_ms: 10, p99_ms: 100, max_ms: 150, buffer_ms: 1, mark_ms: 2, put_ms: 10 },
        { at: at(1), source: "crm", count: 1, p50_ms: 25, p99_ms: 25, max_ms: 25, buffer_ms: 5, mark_ms: 2, put_ms: 2 },
        { at: at(2), source: "esp", count: 0, p50_ms: 0, p99_ms: 0, max_ms: 0, buffer_ms: 0, mark_ms: 0, put_ms: 0 },
      ],
    });
    const rows = ackSeries(data, "");
    expect(rows).toHaveLength(1);
    expect(rows[0]).toMatchObject({ p50: 25, p99: 100, buffer: 2, put: 8 });
    expect(ackSeries(data, "crm")[0]).toMatchObject({ p99: 25 });
  });

  it("lists compactions oldest first in seconds", () => {
    const data = overview({
      compactions: [
        { source: "esp", window_start: at(0), at: at(2), duration_ms: 1500, lag_seconds: 200, segments: 1, events: 1, input_bytes: 1, decoded_bytes: 1, output_bytes: 1 },
        { source: "crm", window_start: at(0), at: at(1), duration_ms: 500, lag_seconds: 100, segments: 1, events: 1, input_bytes: 1, decoded_bytes: 1, output_bytes: 1 },
      ],
    });
    expect(compactionSeries(data, "").map((c) => c.source)).toEqual(["crm", "esp"]);
    expect(compactionSeries(data, "esp")[0]).toMatchObject({ lag: 200, duration: 1.5 });
  });

  it("labels an hourly bucket with its day", () => {
    expect(bucketLabel(at(0), 3600)).not.toEqual(bucketLabel(at(0), 60));
  });
});
