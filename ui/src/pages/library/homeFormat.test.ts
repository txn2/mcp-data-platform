import { describe, expect, it } from "vitest";
import type { LibraryItem } from "@/api/portal/hooks/library";
import { changedWhen, itemDetail } from "./homeFormat";

const now = new Date("2026-11-20T15:00:00Z");

function item(over: Partial<LibraryItem>): LibraryItem {
  return { kind: "asset", id: "a1", name: "Report", at: now.toISOString(), ...over };
}

describe("Home rows say whose an item is and when it changed (#2734)", () => {
  it("names the owner of another person's item", () => {
    const it2 = item({ owner_email: "dana@example.com", updated_at: "2026-11-12T10:00:00Z" });
    expect(itemDetail(it2, "me@example.com", now)).toMatch(/^Shared by dana@example\.com · Changed /);
  });

  it("says Yours for the reader's own, whatever the case of the address", () => {
    expect(itemDetail(item({ owner_email: "Me@Example.com" }), "me@example.com", now)).toBe("Yours");
  });

  it("puts Collection first for a collection", () => {
    expect(itemDetail(item({ kind: "collection", owner_email: "lee@example.com" }), "me@example.com", now)).toBe(
      "Collection · Shared by lee@example.com",
    );
  });

  it("is relative within a day and a date after that, with the year only when it is not this one", () => {
    expect(changedWhen(new Date(now.getTime() - 3 * 60 * 1000).toISOString(), now)).toBe("3m ago");
    const lastWeek = changedWhen("2026-11-12T10:00:00Z", now);
    expect(lastWeek).not.toMatch(/2026/);
    expect(lastWeek).not.toBe("");
    expect(changedWhen("2025-03-01T10:00:00Z", now)).toMatch(/2025/);
    expect(changedWhen("not a time", now)).toBe("");
  });

  it("leaves out what it does not know", () => {
    expect(itemDetail(item({}), "me@example.com", now)).toBe("");
  });
});
