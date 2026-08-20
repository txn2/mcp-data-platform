import type { ScriptRun } from "@/api/portal/hooks/scripts";
import { describe, it, expect } from "vitest";
import {
  dryRunOutputPhrase,
  executionState,
  formatWhen,
  outputLink,
  runStatusLabel,
  runStatusVariant,
  runWhen,
  successRate,
  summarize,
} from "./runFormat";

describe("dryRunOutputPhrase", () => {
  it("names a refresh as a data-region refresh, not a table or a document", () => {
    expect(
      dryRunOutputPhrase({ format: "json", row_count: 0, refresh: true, bytes: 42 }),
    ).toBe("a data-region refresh (42 bytes of JSON)");
  });
  it("keeps the document and table phrasings", () => {
    expect(
      dryRunOutputPhrase({ format: "html", row_count: 0, document: true, bytes: 9 }),
    ).toBe("a html document (9 bytes) to the portal");
    expect(
      dryRunOutputPhrase({ format: "csv", row_count: 1, bytes: 9, destination: "acme-drop" }),
    ).toBe("1 row as csv (9 bytes) to acme-drop");
  });
});

describe("run status", () => {
  it("separates a skipped overlap from both a success and a failure", () => {
    expect(runStatusVariant("succeeded")).toBe("success");
    expect(runStatusVariant("failed")).toBe("danger");
    expect(runStatusVariant("running")).toBe("info");
    // A fire skipped because the previous run was still going is neither: it
    // is a fact about the cadence.
    expect(runStatusVariant("skipped_overlap")).toBe("warning");
    expect(runStatusVariant("pending")).toBe("muted");
  });

  it("renders skipped_overlap in words and leaves the rest alone", () => {
    expect(runStatusLabel("skipped_overlap")).toBe("Skipped (overlap)");
    expect(runStatusLabel("succeeded")).toBe("succeeded");
  });
});

describe("run times", () => {
  it("renders a missing or unparseable timestamp as a dash rather than inventing one", () => {
    expect(formatWhen(undefined)).toBe("—");
    expect(formatWhen("not a date")).toBe("—");
    expect(formatWhen("2026-08-14T07:00:00Z")).not.toBe("—");
  });

  it("reads a run against when it finished, then started, then its fire", () => {
    const fire = "2026-08-12T07:00:00Z";
    const started = "2026-08-13T07:00:00Z";
    const finished = "2026-08-14T07:00:00Z";
    expect(runWhen({ fire_time: fire, started_at: started, finished_at: finished })).toBe(
      formatWhen(finished),
    );
    expect(runWhen({ fire_time: fire, started_at: started })).toBe(formatWhen(started));
    // A run that has not started has only the fire it was created for, which is
    // the honest answer to "when is this for".
    expect(runWhen({ fire_time: fire })).toBe(formatWhen(fire));
  });
});

describe("outputs", () => {
  it("links an asset the platform still serves", () => {
    const link = outputLink({
      name: "daily-sales",
      asset_id: "asset-1",
      asset_version: 42,
      format: "csv",
      row_count: 10,
      bytes: 100,
    });
    expect(link.href).toBe("/assets/asset-1");
    expect(link.detail).toBe("asset version 42");
  });

  it("says a refresh replaced the data region rather than reading as a whole write", () => {
    const link = outputLink({
      name: "revenue-dashboard",
      asset_id: "asset-1",
      asset_version: 43,
      format: "json",
      row_count: 0,
      refresh: true,
      bytes: 100,
    });
    expect(link.href).toBe("/assets/asset-1");
    expect(link.detail).toBe("data refresh, asset version 43");
  });

  it("names a delivered object without offering a link to it", () => {
    const link = outputLink({
      name: "daily-sales",
      destination: "acme-crm-drop",
      bucket: "acme-exports",
      key: "sales/daily.csv",
      format: "csv",
      row_count: 10,
      bytes: 100,
    });
    expect(link.href).toBeUndefined();
    expect(link.detail).toBe("delivered to acme-exports/sales/daily.csv");
  });

  it("falls back to the format for an output with no locator at all", () => {
    const link = outputLink({ name: "x", format: "json", row_count: 0, bytes: 0 });
    expect(link.href).toBeUndefined();
    expect(link.detail).toBe("json");
  });
});

describe("execution state", () => {
  it("reports a script nothing has approved as running nothing", () => {
    const state = executionState({ approval: { approved: false } });
    expect(state.label).toBe("Nothing approved");
    expect(state.variant).toBe("muted");
    expect(state.detail).toMatch(/No version is approved/);
  });

  it("carries the gate's own refusal over an approved version that still will not run", () => {
    const state = executionState({
      approval: { approved: true, version: 3, refusal: "the script is disabled" },
    });
    expect(state.label).toBe("Approved v3");
    expect(state.variant).toBe("warning");
    expect(state.detail).toBe("the script is disabled");
  });

  it("reports an approved, admissible script with no caveat", () => {
    const state = executionState({ approval: { approved: true, version: 3 } });
    expect(state.label).toBe("Approved v3");
    expect(state.variant).toBe("success");
    expect(state.detail).toBeUndefined();
  });
});

describe("what a run history adds up to", () => {
  const run = (status: string, duration_ms: number, id = status): ScriptRun => ({
    id,
    status,
    trigger: "schedule",
    version: 2,
    fire_time: "2026-08-14T07:00:00Z",
    duration_ms,
    output_count: 0,
  });

  it("counts each terminal state and takes the median duration", () => {
    const summary = summarize([
      run("succeeded", 1000, "a"),
      run("failed", 3000, "b"),
      run("succeeded", 2000, "c"),
      run("skipped_overlap", 0, "d"),
    ]);
    expect(summary).toMatchObject({ total: 4, succeeded: 2, failed: 1, skipped: 1, medianMs: 2000 });
    expect(summary.lastFailure?.id).toBe("b");
  });

  it("takes the mean of the middle two on an even count", () => {
    expect(summarize([run("succeeded", 1000, "a"), run("succeeded", 2000, "b")]).medianMs).toBe(1500);
  });

  // A run that never started has no duration to average, and counting it as
  // zero would drag the median toward a number no run took.
  it("ignores runs that recorded no duration", () => {
    expect(summarize([run("skipped_overlap", 0, "a"), run("succeeded", 4000, "b")]).medianMs).toBe(4000);
  });

  // No runs is not a 0% success rate.
  it("has no success rate to report over an empty history", () => {
    expect(successRate(summarize([]))).toBeUndefined();
    expect(successRate(summarize([run("succeeded", 10, "a")]))).toBe(100);
    expect(successRate(summarize([run("failed", 10, "a"), run("succeeded", 10, "b")]))).toBe(50);
  });
});
