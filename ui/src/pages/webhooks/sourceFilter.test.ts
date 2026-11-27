import { describe, expect, it } from "vitest";
import type { WebhookSourceStatus } from "@/api/admin/types";

import { filterSources, isFiltered, kindOptions, matches, readFilter, rejectedLastDay, writeFilter } from "./sourceFilter";

const src = (name: string, input: WebhookSourceStatus["input"], lastDay: Record<string, number> = {}) =>
  ({ name, input, table: `webhook_${name.replace(/-/g, "_")}`, connection: "scratch", last_day: lastDay }) as WebhookSourceStatus;

const list = [
  src("form-acme-contact", "form", { accepted: 4 }),
  src("form-beta-signup", "form", { accepted: 1, bot_check_failed: 3 }),
  src("stripe", "http", { accepted: 90, unauthorized: 1 }),
  src("support-inbox", "imap"),
];

// #2792: what the Webhooks page's search and filters keep.
describe("the sources filter", () => {
  it("matches every word, case-insensitively, in the name, table, connection or kind", () => {
    expect(matches(list[0]!, "ACME contact")).toBe(true);
    expect(matches(list[0]!, "acme beta")).toBe(false);
    expect(matches(list[3]!, "mailbox"), "the kind as the page names it").toBe(true);
    expect(matches(list[2]!, "webhook_stripe")).toBe(true);
    expect(matches(list[2]!, "  ")).toBe(true);
  });

  it("filters by kind and by a rejected request in the last day", () => {
    expect(filterSources(list, { q: "", kind: "form", rejected: false }).map((s) => s.name)).toEqual([
      "form-acme-contact",
      "form-beta-signup",
    ]);
    expect(filterSources(list, { q: "", kind: "", rejected: true }).map((s) => s.name)).toEqual(["form-beta-signup", "stripe"]);
    expect(filterSources(list, { q: "beta", kind: "form", rejected: true }).map((s) => s.name)).toEqual(["form-beta-signup"]);
    expect(rejectedLastDay(list[1]!)).toBe(3);
    expect(rejectedLastDay(list[3]!)).toBe(0);
  });

  it("keeps the filter in the address, leaving out what is empty", () => {
    const params = writeFilter(new URLSearchParams("tab=x"), { q: " acme ", kind: "form", rejected: true });
    expect(params.toString()).toBe("tab=x&q=acme&kind=form&rejected=1");
    expect(readFilter(`?${params.toString()}`)).toEqual({ q: "acme", kind: "form", rejected: true });
    expect(writeFilter(params, { q: "", kind: "", rejected: false }).toString()).toBe("tab=x");
    expect(isFiltered({ q: " ", kind: "", rejected: false })).toBe(false);
    expect(isFiltered({ q: "", kind: "", rejected: true })).toBe(true);
  });

  it("offers the kinds present, and one the address names", () => {
    expect(kindOptions(list).map((o) => o.label)).toEqual(["All kinds", "Inbound", "Form", "Mailbox"]);
    expect(kindOptions(list, "postgres").map((o) => o.value)).toContain("postgres");
    expect(kindOptions([src("x", "other" as WebhookSourceStatus["input"])]).map((o) => o.label)).toEqual(["All kinds", "other"]);
  });
});
