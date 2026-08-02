import { describe, it, expect } from "vitest";
import { placeLabels, type LabelCandidate } from "./labelPlacement";

function candidate(over: Partial<LabelCandidate> & { id: string }): LabelCandidate {
  return { text: over.id, x: 0, y: 0, radius: 6, priority: 0, ...over };
}

describe("placeLabels", () => {
  it("draws a label that collides with nothing", () => {
    const shown = placeLabels([candidate({ id: "solo" })]);
    expect([...shown]).toEqual(["solo"]);
  });

  it("drops the lower-priority label of an overlapping pair", () => {
    // Two nodes a few pixels apart: their labels would sit on top of each other.
    const shown = placeLabels([
      candidate({ id: "important", text: "Fiscal Calendar", x: 100, y: 100, priority: 10 }),
      candidate({ id: "minor", text: "Revenue Definition", x: 106, y: 100, priority: 1 }),
    ]);

    expect(shown.has("important")).toBe(true);
    expect(shown.has("minor")).toBe(false);
  });

  it("keeps both when they are far enough apart", () => {
    const shown = placeLabels([
      candidate({ id: "a", text: "Fiscal Calendar", x: 0, y: 0 }),
      candidate({ id: "b", text: "Fiscal Calendar", x: 400, y: 0 }),
    ]);
    expect(shown.size).toBe(2);
  });

  it("separates labels stacked vertically by more than the line height", () => {
    const shown = placeLabels([
      candidate({ id: "a", text: "Same Name", x: 0, y: 0 }),
      candidate({ id: "b", text: "Same Name", x: 0, y: 40 }),
    ]);
    expect(shown.size).toBe(2);
  });

  it("never drops a label the reader explicitly asked for", () => {
    // The selection sits under a much more important node; hiding its label
    // would read as the selection having failed.
    const shown = placeLabels(
      [
        candidate({ id: "hub", text: "Very Important Hub", x: 100, y: 100, priority: 99 }),
        candidate({ id: "selected", text: "Chosen Node", x: 102, y: 100, priority: 0 }),
      ],
      new Set(["selected"]),
    );

    expect(shown.has("selected")).toBe(true);
  });

  it("places every always-show label even when they collide with each other", () => {
    const forced = new Set(["a", "b"]);
    const shown = placeLabels(
      [
        candidate({ id: "a", text: "Overlapping", x: 100, y: 100 }),
        candidate({ id: "b", text: "Overlapping", x: 102, y: 100 }),
      ],
      forced,
    );
    expect(shown).toEqual(forced);
  });

  it("is deterministic when priorities tie", () => {
    const build = () => [
      candidate({ id: "zzz", text: "Colliding Label", x: 100, y: 100 }),
      candidate({ id: "aaa", text: "Colliding Label", x: 103, y: 100 }),
    ];
    expect([...placeLabels(build())]).toEqual([...placeLabels(build())]);
    // The tiebreak is the id, so the result does not depend on input order.
    expect([...placeLabels(build().reverse())]).toEqual([...placeLabels(build())]);
  });

  it("skips a node with no text rather than reserving space for it", () => {
    const shown = placeLabels([
      candidate({ id: "blank", text: "", x: 100, y: 100, priority: 99 }),
      candidate({ id: "real", text: "Fiscal Calendar", x: 100, y: 100, priority: 1 }),
    ]);
    expect(shown.has("blank")).toBe(false);
    expect(shown.has("real")).toBe(true);
  });
});
