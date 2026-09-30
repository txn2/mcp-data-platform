import { describe, expect, it } from "vitest";
import type { WebhookStatusOverview } from "@/api/admin/types";
import { outcomeLabel, splitCounts, volumeSeries } from "./webhookOverview";

const base: WebhookStatusOverview = {
  generated_at: "2026-09-29T12:00:30Z",
  range: "hour",
  from: "2026-09-29T11:57:30Z",
  bucket_seconds: 60,
  silent_after_seconds: 86400,
  sources: [],
  volume: [],
  rejections: [],
};

describe("volumeSeries", () => {
  it("keeps a day's first bucket when the minute after from falls inside it", () => {
    const day = { ...base, range: "day" as const, bucket_seconds: 900, from: "2026-09-28T12:07:30Z" };
    expect(volumeSeries(day, "").buckets[0]?.at).toBe("2026-09-28T12:00:00.000Z");
  });

  it("has one zeroed bucket per step from the first whole minute counted to the one holding now", () => {
    // from is 11:57:30, and counts are read from 11:58: a bar for 11:57 would
    // always be empty.
    const { buckets, outcomes, total } = volumeSeries(base, "");
    expect(buckets.map((b) => b.at)).toEqual([
      "2026-09-29T11:58:00.000Z",
      "2026-09-29T11:59:00.000Z",
      "2026-09-29T12:00:00.000Z",
    ]);
    expect(outcomes).toEqual([]);
    expect(total).toBe(0);
  });

  it("sums every source's points into their buckets, or one source's, ordering known outcomes first", () => {
    const ov: WebhookStatusOverview = {
      ...base,
      volume: [
        { at: "2026-09-29T11:58:00Z", source: "a", outcome: "unknown_source", count: 1 },
        { at: "2026-09-29T11:58:00Z", source: "a", outcome: "unauthorized", count: 2 },
        { at: "2026-09-29T11:58:00Z", source: "a", outcome: "accepted", count: 3 },
        { at: "2026-09-29T11:58:00Z", source: "b", outcome: "accepted", count: 4 },
        { at: "2026-09-29T12:00:00Z", source: "b", outcome: "accepted", count: 5 },
      ],
    };
    const all = volumeSeries(ov, "");
    expect(all.outcomes).toEqual(["accepted", "unauthorized", "unknown_source"]);
    expect(all.total).toBe(15);
    expect(all.buckets[0]).toEqual({ at: "2026-09-29T11:58:00.000Z", accepted: 7, unauthorized: 2, unknown_source: 1 });
    expect(all.buckets[1]).toEqual({ at: "2026-09-29T11:59:00.000Z", accepted: 0, unauthorized: 0, unknown_source: 0 });

    const b = volumeSeries(ov, "b");
    expect(b.outcomes).toEqual(["accepted"]);
    expect(b.total).toBe(9);
    expect(b.buckets.map((x) => x.accepted)).toEqual([4, 0, 5]);
  });

  it("keeps a point outside the expected buckets rather than dropping it", () => {
    const ov = { ...base, volume: [{ at: "2026-09-29T11:00:00Z", source: "a", outcome: "accepted", count: 2 }] };
    const { buckets, total } = volumeSeries(ov, "");
    expect(total).toBe(2);
    expect(buckets[0]).toEqual({ at: "2026-09-29T11:00:00.000Z", accepted: 2 });
    expect(buckets).toHaveLength(4);
  });
});

describe("outcomeLabel", () => {
  it("names known outcomes and spells out unknown ones", () => {
    expect(outcomeLabel("buffer_full")).toBe("Buffer full");
    expect(outcomeLabel("unknown_source")).toBe("Unknown source");
  });
});

describe("splitCounts", () => {
  it("counts accepted apart from every other outcome", () => {
    expect(splitCounts({ accepted: 5, unauthorized: 2, too_large: 1 })).toEqual({ accepted: 5, rejected: 3 });
    expect(splitCounts({})).toEqual({ accepted: 0, rejected: 0 });
  });
});
