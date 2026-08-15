import { describe, it, expect } from "vitest";
import {
  executionState,
  formatWhen,
  outputLink,
  runStatusLabel,
  runStatusVariant,
  runWhen,
} from "./runFormat";

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
