import { describe, expect, it } from "vitest";

import { formatLabel, isRegistrableType } from "./types";

describe("which stored files a table can be registered over", () => {
  it("takes a CSV and JSON lines under any of their names", () => {
    for (const ct of [
      "text/csv",
      "text/csv; charset=utf-8",
      "application/csv",
      "application/x-ndjson",
      "application/ndjson",
      "application/jsonl",
      "text/x-ndjson",
    ]) {
      expect(isRegistrableType(ct), ct).toBe(true);
    }
  });

  it("takes a JSON-lines file by its name where the type does not say", () => {
    expect(isRegistrableType("application/json", "rows.jsonl")).toBe(true);
    expect(isRegistrableType("text/plain", "rows.ndjson")).toBe(true);
    expect(isRegistrableType("application/octet-stream", "keys.csv")).toBe(true);
    expect(isRegistrableType("", "rows.JSONL")).toBe(true);
    expect(isRegistrableType("application/json", "rows.json")).toBe(false);
    expect(isRegistrableType("text/html", "rows.jsonl")).toBe(false);
  });

  it("refuses everything else, JSON documents included", () => {
    for (const ct of ["application/json", "text/html", "text/plain", ""]) {
      expect(isRegistrableType(ct), ct).toBe(false);
    }
  });

  it("names each format the way a reader says it", () => {
    expect(formatLabel("jsonl")).toBe("JSON lines");
    expect(formatLabel("csv")).toBe("CSV");
    expect(formatLabel(undefined)).toBe("CSV");
  });
});
