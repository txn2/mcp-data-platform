import { describe, expect, it } from "vitest";
import {
  folderEntries,
  folderOfKey,
  freshFolderName,
  nextSort,
  pickRange,
  shortDate,
  sortDirection,
  sortFolders,
} from "./model";

const folders = [
  { path: "data", count: 3, updated_at: "2026-09-01T00:00:00Z" },
  { path: "data/weekly", count: 2, updated_at: "2026-09-10T00:00:00Z" },
  { path: "data/archive", count: 0, updated_at: "2026-08-01T00:00:00Z" },
  { path: "data/weekly/old", count: 1 },
  { path: "brand", count: 1 },
];

describe("the folder rows of one level", () => {
  it("are the folders directly inside it, deeper ones counted in them", () => {
    expect(folderEntries(folders, "").map((e) => e.key)).toEqual(["d:data", "d:brand"]);
    expect(folderEntries(folders, "data").map((e) => e.name)).toEqual(["weekly", "archive"]);
    expect(folderOfKey("d:data/weekly")).toBe("data/weekly");
    expect(folderOfKey("res-1")).toBeNull();
  });

  it("follow the column the files are sorted by", () => {
    const level = folderEntries(folders, "data");
    expect(sortFolders(level, "name").map((e) => e.name)).toEqual(["archive", "weekly"]);
    expect(sortFolders(level, "name_desc").map((e) => e.name)).toEqual(["weekly", "archive"]);
    expect(sortFolders(level, "updated").map((e) => e.name)).toEqual(["weekly", "archive"]);
    expect(sortFolders(level, "updated_asc").map((e) => e.name)).toEqual(["archive", "weekly"]);
    expect(sortFolders(level, "size").map((e) => e.name)).toEqual(["archive", "weekly"]);
    expect(sortFolders(level, "size_desc").map((e) => e.name)).toEqual(["weekly", "archive"]);
  });
});

describe("a header click", () => {
  it("starts Name ascending and the others newest or largest first, then toggles", () => {
    expect(nextSort("name", "updated")).toBe("name");
    expect(nextSort("name", "name")).toBe("name_desc");
    expect(nextSort("modified", "name")).toBe("updated");
    expect(nextSort("modified", "updated")).toBe("updated_asc");
    expect(nextSort("size", "name")).toBe("size_desc");
    expect(nextSort("size", "size_desc")).toBe("size");
    expect(sortDirection("size", "size_desc")).toBe("desc");
    expect(sortDirection("name", "size")).toBeNull();
    // Recently read has one order, newest first.
    expect(nextSort("lastRead", "name")).toBe("last_read");
    expect(nextSort("lastRead", "last_read")).toBe("last_read");
    expect(sortDirection("lastRead", "last_read")).toBe("desc");
  });
});

describe("a Shift-click range", () => {
  it("runs from the anchor to the row, either way, and is the row alone with no anchor", () => {
    const keys = ["a", "b", "c", "d"];
    expect(pickRange(keys, "b", "d")).toEqual(["b", "c", "d"]);
    expect(pickRange(keys, "d", "b")).toEqual(["b", "c", "d"]);
    expect(pickRange(keys, null, "c")).toEqual(["c"]);
    expect(pickRange(keys, "gone", "c")).toEqual(["c"]);
  });
});

describe("small words", () => {
  it("names a new folder that is not taken", () => {
    expect(freshFolderName([])).toBe("New folder");
    expect(freshFolderName(["New folder", "New folder 2"])).toBe("New folder 3");
  });

  it("dates a change relative to now, then by the calendar", () => {
    const now = Date.parse("2026-09-24T12:00:00Z");
    expect(shortDate("2026-09-24T08:00:00Z", now)).toBe("Today");
    expect(shortDate("2026-09-23T08:00:00Z", now)).toBe("Yesterday");
    expect(shortDate("2026-09-20T12:00:00Z", now)).toBe("4 days ago");
    expect(shortDate("2026-01-02T12:00:00Z", now)).toContain("2026");
    expect(shortDate(undefined, now)).toBe("");
    expect(shortDate("not a date", now)).toBe("");
  });
});
