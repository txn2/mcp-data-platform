import { describe, it, expect } from "vitest";
import { formatBytes } from "./format";

// The unit list stopped at MB, so anything a gigabyte or over rendered as
// "2 undefined". Reachable from any stored file that size, and from a
// deployment that raises resources.managed.max_upload_bytes past a gigabyte,
// which the upload dialog states with this function (#1628).
describe("formatBytes", () => {
  it.each([
    [0, "0 B"],
    [512, "512 B"],
    [1024, "1 KB"],
    [104857600, "100 MB"],
    [262144000, "250 MB"],
    [1024 ** 3 * 2, "2 GB"],
    [1024 ** 4 * 3, "3 TB"],
  ])("renders %i as %s", (bytes, want) => {
    expect(formatBytes(bytes)).toBe(want);
  });

  it("keeps scaling in the last unit rather than naming one it does not have", () => {
    expect(formatBytes(1024 ** 5)).toBe("1024 TB");
  });

  // The server writes an upload refusal with the same units and the same one
  // decimal place (resource.DescribeUploadLimit), so a refusal and the chooser
  // beside it name the same ceiling identically.
  it("renders a fractional value to one decimal place", () => {
    expect(formatBytes(1.5 * 1024 * 1024)).toBe("1.5 MB");
  });

  // A rate of 0.149 B/day read as "152.2 undefined" (#2201): below one byte
  // the unit index went to -1.
  it("names a value under one byte without a unit it does not have", () => {
    expect(formatBytes(0.149)).toBe("< 1 B");
  });

  it("keeps the sign of a negative value", () => {
    expect(formatBytes(-2048)).toBe("-2 KB");
    expect(formatBytes(-0.149)).toBe("< 1 B");
  });
});
