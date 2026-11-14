import { describe, it, expect } from "vitest";
import { handlerListQuery, type AdminHandlerListParams } from "./handlers";

// Admin > Handlers (#2435) asks the server for one page of the handlers its
// filters match; GET /admin/handlers reads type, search, owner, enabled, limit
// and offset (internal/httpserver/scripthttp/handlerhttp).

const none: AdminHandlerListParams = { type: "", search: "", owner: "", state: "", page: 1, perPage: 50 };

function parsed(p: Partial<AdminHandlerListParams>): Record<string, string> {
  return Object.fromEntries(new URLSearchParams(handlerListQuery({ ...none, ...p })));
}

describe("handlerListQuery", () => {
  it("asks for the first page and nothing else when nothing is narrowed", () => {
    expect(handlerListQuery(none)).toBe("limit=50");
  });

  it("names each filter the server reads", () => {
    expect(parsed({ type: "form" })).toEqual({ type: "form", limit: "50" });
    expect(parsed({ owner: "sarah.chen@example.com" })).toEqual({ owner: "sarah.chen@example.com", limit: "50" });
    expect(parsed({ search: "orders" })).toEqual({ search: "orders", limit: "50" });
  });

  it("reads On and Off as enabled true and false", () => {
    expect(parsed({ state: "on" })).toEqual({ enabled: "true", limit: "50" });
    expect(parsed({ state: "off" })).toEqual({ enabled: "false", limit: "50" });
  });

  it("trims the search, and sends no search of spaces", () => {
    expect(parsed({ search: "  shop orders " })).toEqual({ search: "shop orders", limit: "50" });
    expect(parsed({ search: "   " })).toEqual({ limit: "50" });
  });

  it("skips the pages before the one asked for", () => {
    expect(parsed({ page: 1 })).not.toHaveProperty("offset");
    expect(parsed({ page: 3, perPage: 25 })).toEqual({ limit: "25", offset: "50" });
  });

  it("combines every filter in one query, encoded", () => {
    expect(handlerListQuery({ type: "webhook", search: "a&b", owner: "o@example.com", state: "on", page: 2, perPage: 50 })).toBe(
      "type=webhook&search=a%26b&owner=o%40example.com&enabled=true&limit=50&offset=50",
    );
  });
});
