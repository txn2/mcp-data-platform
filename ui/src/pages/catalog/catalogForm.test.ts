import { describe, expect, it } from "vitest";
import { connectionOptions, formFromSettings, parseEntries, settingsFromForm } from "./catalogForm";

describe("catalogForm", () => {
  it("reads comma- and line-separated prefixes", () => {
    expect(parseEntries(" hive.tmp, pg\nsystem ,, ")).toEqual(["hive.tmp", "pg", "system"]);
    expect(parseEntries("")).toEqual([]);
  });

  it("round-trips settings through the form", () => {
    const form = formFromSettings("6h", [{ connection_kind: "trino", connection: "acme", entries: ["hive.tmp", "pg"] }]);
    expect(form.rows).toEqual([{ connection: "trino/acme", leaveOut: "hive.tmp, pg" }]);
    expect(settingsFromForm(form)).toEqual({
      scan_interval: "6h",
      exclusions: [{ connection_kind: "trino", connection: "acme", entries: ["hive.tmp", "pg"] }],
    });
  });

  it("drops a row that names no connection or leaves nothing out", () => {
    const out = settingsFromForm({
      scanEvery: " 2h ",
      rows: [
        { connection: "", leaveOut: "a" },
        { connection: "trino/acme", leaveOut: " , " },
        { connection: "graphql/gh", leaveOut: "mutation" },
      ],
    });
    expect(out).toEqual({
      scan_interval: "2h",
      exclusions: [{ connection_kind: "graphql", connection: "gh", entries: ["mutation"] }],
    });
  });

  it("lists every connection once, by name", () => {
    const opts = connectionOptions(
      [
        { connection_kind: "trino", connection: "acme", started_at: "", finished_at: "", datasets: 0, removed: 0, unread: [] },
        { connection_kind: "api", connection: "stripe", started_at: "", finished_at: "", datasets: 0, removed: 0, unread: [] },
      ],
      [
        { kind: "trino", name: "acme", config: {}, description: "", created_by: "", updated_at: "" },
        { kind: "graphql", name: "gh", config: {}, description: "", created_by: "", updated_at: "" },
      ],
    );
    expect(opts).toEqual([
      { value: "trino/acme", label: "acme (trino)" },
      { value: "graphql/gh", label: "gh (graphql)" },
      { value: "api/stripe", label: "stripe (api)" },
    ]);
    expect(connectionOptions(undefined, undefined)).toEqual([]);
  });
});
