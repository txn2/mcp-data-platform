import { describe, expect, it } from "vitest";
import { cellText, compareCells, detailText } from "./cellText";

describe("cellText", () => {
  it("writes a nested value as its JSON, not [object Object]", () => {
    expect(cellText({ a: 1, b: [2, 3] })).toBe('{"a":1,"b":[2,3]}');
    expect(detailText({ a: 1 })).toBe('{\n  "a": 1\n}');
  });

  it("writes a BigInt as its digits, alone and inside a value", () => {
    expect(cellText(9007199254740993n)).toBe("9007199254740993");
    expect(cellText({ id: 9007199254740993n })).toBe('{"id":"9007199254740993"}');
  });

  it("writes bytes as hex and a date as ISO", () => {
    expect(cellText(new Uint8Array([0, 1, 255]))).toBe("0x0001ff");
    expect(cellText(new Date(Date.UTC(2024, 4, 1, 12)))).toBe("2024-05-01T12:00:00.000Z");
    expect(detailText(new Date(Date.UTC(2024, 4, 1)))).toBe("2024-05-01T00:00:00.000Z");
  });

  it("writes nothing for a null and a scalar as itself", () => {
    expect(cellText(null)).toBe("");
    expect(cellText(undefined)).toBe("");
    expect(cellText(false)).toBe("false");
    expect(cellText(1.5)).toBe("1.5");
  });
});

describe("compareCells", () => {
  it("orders numbers and BigInts by magnitude and anything else as text", () => {
    expect(compareCells(2, 10)).toBeLessThan(0);
    expect(compareCells(10n, 2n)).toBeGreaterThan(0);
    expect(compareCells(3n, 3)).toBe(0);
    expect(compareCells("b", "a")).toBeGreaterThan(0);
  });
});
