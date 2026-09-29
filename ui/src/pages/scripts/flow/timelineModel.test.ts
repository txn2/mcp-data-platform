import { describe, expect, it } from "vitest";
import type { FlowTimedCall } from "@/api/portal/hooks/scriptFlow";
import { buildTimeline, busy, fnAt, lineOf } from "./timelineModel";

const functions = [
  { name: "fetch", line: 2, end_line: 4 },
  { name: "stage", line: 6, end_line: 12 },
  { name: "main", line: 14, end_line: 20 },
];

function call(start: number, dur: number, site?: string[], over: Partial<FlowTimedCall> = {}): FlowTimedCall {
  return { start_ms: start, duration_ms: dur, tool: "api_invoke_endpoint", success: true, response_chars: 1, call_site: site, ...over };
}

describe("timelineModel", () => {
  it("names the innermost function a line is in", () => {
    expect(fnAt(functions, 3)).toBe("fetch()");
    expect(fnAt(functions, 16)).toBe("main()");
    expect(fnAt([...functions, { name: "inner", line: 8, end_line: 9 }], 8)).toBe("inner()");
    expect(fnAt(functions, 99)).toBe("line 99");
    expect(lineOf("12:5")).toBe(12);
    expect(lineOf("x")).toBe(0);
  });

  it("puts a helper's consecutive calls under one frame, and a new call site under a new one", () => {
    const t = buildTimeline(
      [
        call(0, 10, ["15:3", "8:5", "3:9"]),
        call(20, 10, ["15:3", "9:5", "3:9"]),
        call(40, 5, ["16:3"]),
        call(50, 5, ["15:3", "8:5", "3:9"]),
      ],
      functions,
      100,
    );
    const frames = t.bars.filter((b) => !b.call);
    expect(frames.map((b) => [b.depth, b.label, b.start, b.end])).toEqual([
      [0, "main()", 0, 100],
      [1, "stage()", 0, 30],
      [2, "fetch()", 0, 10],
      [2, "fetch()", 20, 30],
      [1, "stage()", 50, 55],
      [2, "fetch()", 50, 55],
    ]);
    expect(t.bars.filter((b) => b.call).map((b) => b.depth)).toEqual([3, 3, 1, 3]);
    expect(t.depth).toBe(4);
    expect(t.span).toBe(100);
    expect(t.upstream).toBe(30);
  });

  it("draws calls with no call site in one row under main()", () => {
    const t = buildTimeline([call(0, 5), call(10, 5, [])], functions, 0);
    expect(t.bars.filter((b) => b.call).map((b) => b.depth)).toEqual([1, 1]);
    expect(t.span).toBe(15);
    expect(t.depth).toBe(2);
  });

  it("measures the time some call was in flight, overlaps counted once", () => {
    expect(busy([])).toBe(0);
    expect(busy([call(0, 10), call(5, 10), call(30, 5)])).toBe(20);
  });
});
