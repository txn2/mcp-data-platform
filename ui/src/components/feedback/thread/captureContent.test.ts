import { describe, expect, it } from "vitest";
import type { ThreadEvent } from "@/api/portal/types";
import { checkInsightText, defaultInsightContent, insightBytes } from "./captureContent";

const event = (body?: string) => ({ body }) as ThreadEvent;

describe("defaultInsightContent", () => {
  it("joins the title and the first message", () => {
    expect(defaultInsightContent(" Title ", [event(" first "), event("second")])).toBe("Title\n\nfirst");
  });
  it("skips events with no text", () => {
    expect(defaultInsightContent("", [event(undefined), event("  "), event("the message")])).toBe("the message");
  });
  it("uses the title when there is no message", () => {
    expect(defaultInsightContent("Only a title", [])).toBe("Only a title");
  });
  it("is empty before the events arrive and with no title", () => {
    expect(defaultInsightContent(undefined, undefined)).toBe("");
  });
});

describe("insightBytes", () => {
  it("counts UTF-8 bytes of the trimmed text, as the server does", () => {
    expect(insightBytes("  é  ")).toBe(2);
    expect(insightBytes("日本")).toBe(6);
  });
});

describe("checkInsightText", () => {
  it("accepts text at each bound", () => {
    expect(checkInsightText("a".repeat(10)).ok).toBe(true);
    expect(checkInsightText("a".repeat(4000)).ok).toBe(true);
    expect(checkInsightText("a".repeat(4000)).count).toBe("4,000 of 4,000 bytes");
    expect(checkInsightText("a".repeat(4000)).hint).toBe("The insight is created from this text.");
  });
  it("refuses one byte past the limit and says by how much", () => {
    const got = checkInsightText("a".repeat(4001));
    expect(got).toMatchObject({ ok: false, bad: true, over: true, count: "4,001 of 4,000 bytes" });
    expect(got.hint).toBe("1 bytes over the limit. Shorten the text to capture this.");
  });
  it("measures characters of more than one byte", () => {
    const got = checkInsightText("é".repeat(2179));
    expect(got.ok).toBe(false);
    expect(got.hint).toBe("358 bytes over the limit. Shorten the text to capture this.");
  });
  it("refuses text under the minimum without marking the count", () => {
    expect(checkInsightText("too short")).toMatchObject({ ok: false, bad: true, over: false, hint: "Write at least 10 bytes." });
  });
});
