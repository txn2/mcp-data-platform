import { describe, it, expect } from "vitest";
import { DRAG_FOLDER, DRAG_RESOURCE, dragResources, readDrag } from "./drag";

/** A DataTransfer stand-in: jsdom does not implement one. */
function transfer(): DataTransfer {
  const store = new Map<string, string>();
  return {
    setData: (type: string, value: string) => store.set(type, value),
    getData: (type: string) => store.get(type) ?? "",
    effectAllowed: "none",
  } as unknown as DataTransfer;
}

describe("what a drag carries", () => {
  it("carries the whole selection when a picked row is dragged", () => {
    const data = transfer();
    dragResources(data, "b", ["a", "b", "c"]);
    expect(readDrag(data)).toEqual({ kind: "resources", ids: ["a", "b", "c"] });
  });

  // Dragging a row outside the selection carries just it, which is what every
  // file manager does -- and it stops a drag from silently discarding a
  // selection the person has just made.
  it("carries only the dragged row when it is not part of the selection", () => {
    const data = transfer();
    dragResources(data, "z", ["a", "b"]);
    expect(readDrag(data)).toEqual({ kind: "resources", ids: ["z"] });
  });

  it("reads a folder drag as a folder", () => {
    const data = transfer();
    data.setData(DRAG_FOLDER, "data/weekly");
    expect(readDrag(data)).toEqual({ kind: "folder", path: "data/weekly" });
  });

  // A drop target has to be able to tell that it was handed something it does
  // not understand -- a file from the desktop, a text selection -- rather than
  // acting on an empty id list.
  it("reads anything else as nothing it can act on", () => {
    expect(readDrag(transfer())).toEqual({ kind: "none" });
  });

  it("gives the two kinds distinct transfer types", () => {
    expect(DRAG_RESOURCE).not.toBe(DRAG_FOLDER);
  });
});
