import { describe, it, expect } from "vitest";
import { agoText, endpointTail, endpointURL, exactWhen, nextFireText } from "./listingFormat";

// #2601: the Automations listing's short forms. None prints seconds.

const now = new Date("2026-11-10T12:00:00");

describe("agoText", () => {
  it("is relative within a day and a short date after it", () => {
    expect(agoText(new Date("2026-11-10T11:59:40").toISOString(), now)).toBe("just now");
    expect(agoText(new Date("2026-11-10T11:48:00").toISOString(), now)).toBe("12m ago");
    expect(agoText(new Date("2026-11-10T07:00:00").toISOString(), now)).toBe("5h ago");
    const older = agoText(new Date("2026-11-08T09:00:00").toISOString(), now);
    expect(older).toMatch(/Nov/);
    expect(older).not.toMatch(/:\d\d:\d\d/);
    expect(agoText(new Date("2025-11-08T09:00:00").toISOString(), now)).toMatch(/2025/);
  });

  it("is a dash for no moment or one that will not parse", () => {
    expect(agoText(undefined, now)).toBe("—");
    expect(agoText("not a date", now)).toBe("—");
  });
});

describe("nextFireText", () => {
  it("is the time alone today, and the date and time otherwise, never with seconds", () => {
    const today = nextFireText(new Date("2026-11-10T19:30:00").toISOString(), now);
    expect(today).not.toMatch(/Nov/);
    expect(today).toMatch(/7:30/);
    const later = nextFireText(new Date("2026-11-11T07:30:00").toISOString(), now);
    expect(later).toMatch(/Nov 11/);
    expect(later).not.toMatch(/:\d\d:\d\d/);
    expect(nextFireText(undefined, now)).toBe("—");
  });
});

describe("exactWhen", () => {
  it("is the date and minute for a hover title, and nothing for none", () => {
    const full = exactWhen(new Date("2026-11-10T06:58:31").toISOString());
    expect(full).toMatch(/2026/);
    expect(full).not.toMatch(/:31/);
    expect(exactWhen(undefined)).toBe("");
  });
});

describe("endpoint display", () => {
  it("drops the prefix every endpoint shares", () => {
    expect(endpointTail("/endpoints/order-intake/v1/stores/{store_id}")).toBe("order-intake/v1/stores/{store_id}");
    expect(endpointTail("/forms/store-survey")).toBe("forms/store-survey");
  });

  it("is the full address on hover", () => {
    expect(endpointURL("/endpoints/store-survey", "https://data.example.com")).toBe("https://data.example.com/endpoints/store-survey");
    expect(endpointURL("endpoints/x", "https://data.example.com")).toBe("https://data.example.com/endpoints/x");
  });
});
