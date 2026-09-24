import { describe, expect, it } from "vitest";
import { summarize, summaryText } from "./progress";

describe("summarize", () => {
  it("counts every state and outcome", () => {
    const s = summarize([
      { state: "done", outcome: "created" },
      { state: "done", outcome: "created" },
      { state: "done", outcome: "revised" },
      { state: "done", outcome: "unchanged" },
      { state: "failed", error: "x" },
      { state: "refused", error: "y" },
      { state: "ready" },
      { state: "sending", progress: 0.5 },
    ]);
    expect(s).toEqual({ created: 2, revised: 1, unchanged: 1, failed: 1, refused: 1, pending: 2 });
    expect(summaryText(s)).toBe("2 created, 1 new version, 1 unchanged, 1 failed, 1 not sent");
  });

  it("states an empty batch and an all-failed one", () => {
    expect(summaryText(summarize([]))).toBe("0 created, 0 new versions, 0 unchanged, 0 failed");
    expect(summaryText(summarize([{ state: "failed", error: "a" }, { state: "failed", error: "b" }]))).toBe(
      "0 created, 0 new versions, 0 unchanged, 2 failed",
    );
  });
});
