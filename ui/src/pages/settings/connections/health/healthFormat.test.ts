import { describe, expect, it } from "vitest";
import { mockAppDBHealth, mockAppDBHealthSettings } from "@/mocks/handlers/appdb";
import {
  ago,
  bytes,
  formatSQL,
  levelOf,
  ms,
  passedChecks,
  plural,
  shortVersion,
  sinceDay,
  sqlTokens,
} from "./healthFormat";

describe("healthFormat (#2528)", () => {
  it("counts in the singular and the plural", () => {
    expect(plural(1, "call")).toBe("1 call");
    expect(plural(3, "call")).toBe("3 calls");
    expect(plural(18402, "call")).toBe("18,402 calls");
  });

  it("writes sizes, durations and the short version", () => {
    expect(bytes(512)).toBe("512 B");
    expect(bytes(7.5 * 1024 * 1024)).toBe("7.5 MiB");
    expect(bytes(9 * 1024 * 1024)).toBe("9 MiB");
    expect(ms(2.4)).toBe("2.4 ms");
    expect(ms(56)).toBe("56 ms");
    expect(ms(4100)).toBe("4.1 s");
    expect(shortVersion("17.5 (Debian 17.5-1.pgdg120+1)")).toBe("PostgreSQL 17.5");
    expect(shortVersion("odd")).toBe("odd");
  });

  it("writes how long ago", () => {
    const now = Date.parse("2025-01-23T10:00:00Z");
    expect(ago("2025-01-23T10:00:00Z", now)).toBe("just now");
    expect(ago("2025-01-23T09:59:46Z", now)).toBe("14 s ago");
    expect(ago("2025-01-23T09:57:00Z", now)).toBe("3 min ago");
    expect(sinceDay(undefined, now)).toBe("Never");
    expect(sinceDay("2025-01-23T08:00:00Z", now)).toBe("2 h ago");
    expect(sinceDay("2025-01-22T06:00:00Z", now)).toBe("Yesterday");
    expect(sinceDay("2025-01-20T10:00:00Z", now)).toBe("3 days ago");
  });

  it("judges a value against thresholds, 0 being off", () => {
    expect(levelOf({ warning: 80, critical: 95 }, 50)).toBe("");
    expect(levelOf({ warning: 80, critical: 95 }, 80)).toBe("warning");
    expect(levelOf({ warning: 80, critical: 95 }, 99)).toBe("critical");
    expect(levelOf({ warning: 0, critical: 0 }, 1e9)).toBe("");
    expect(levelOf(undefined, 1)).toBe("");
  });

  it("names the checks that passed", () => {
    const clean = { ...mockAppDBHealth, findings: [], sessions: [], indexes: { unused: [], duplicate: [], invalid: [] } };
    expect(passedChecks(clean, mockAppDBHealthSettings, { objects: 0, unchecked: 0 })).toEqual([
      "Indexes valid, none duplicated",
      "No unused indexes",
      "No blocked sessions",
      "No long-running or idle sessions",
      "Dead rows under 20% everywhere",
      "Schema matches migrations",
    ]);
    expect(passedChecks(mockAppDBHealth, mockAppDBHealthSettings, { objects: 2, unchecked: 0 })).not.toContain(
      "Schema matches migrations",
    );
  });

  it("lays a statement out a clause per line and marks its keywords", () => {
    expect(formatSQL("SELECT a FROM t  WHERE b = $1 ORDER BY a")).toBe("SELECT a\nFROM t\nWHERE b = $1\nORDER BY a");
    expect(formatSQL("SELECT 'a FROM b' FROM t")).toBe("SELECT 'a FROM b' FROM t");
    expect(sqlTokens("SELECT a FROM t").filter((t) => t.keyword).map((t) => t.text)).toEqual(["SELECT", "FROM"]);
  });
});
