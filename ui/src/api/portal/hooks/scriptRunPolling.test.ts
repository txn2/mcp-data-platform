import { describe, it, expect } from "vitest";
import type { ScriptRun } from "./scripts";
import { hasRunInFlight } from "./scripts";

// A run asked for on the page is queued and executed by a worker, so the answer
// arrives after the request that started it (#1363). The history therefore
// re-reads itself while anything is in flight and stops the moment nothing is,
// which is what keeps a page of finished runs at one request.

function history(...statuses: string[]): { data: ScriptRun[] } {
  return { data: statuses.map((status) => ({ status }) as ScriptRun) };
}

describe("hasRunInFlight", () => {
  it("is true while a run is pending or running", () => {
    expect(hasRunInFlight(history("pending"))).toBe(true);
    expect(hasRunInFlight(history("running"))).toBe(true);
    expect(hasRunInFlight(history("succeeded", "pending"))).toBe(true);
  });

  it("is false once every run has stopped moving", () => {
    expect(hasRunInFlight(history("succeeded", "failed", "skipped_overlap"))).toBe(false);
    expect(hasRunInFlight(history())).toBe(false);
    expect(hasRunInFlight(undefined)).toBe(false);
  });
});
