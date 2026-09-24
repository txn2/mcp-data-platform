import { beforeEach, describe, expect, it, vi } from "vitest";

const fetchJSON = vi.hoisted(() => vi.fn());
const fetchRaw = vi.hoisted(() => vi.fn());
vi.mock("@/api/resources/client", () => ({ resourceFetch: fetchJSON, resourceFetchRaw: fetchRaw }));

import type { Resource } from "@/api/resources/types";
import type { ResourceRoot } from "../scopes";
import { deleteMany, filesUnder, MAX_FOLDER_FILES, moveMany, summarize, undoMoves } from "./actions";

const root: ResourceRoot = {
  key: "global",
  label: "Global",
  target: { scope: "global", scope_id: "" },
  params: { scope: "global" },
};

function res(id: string, path: string): Resource {
  return { id, path, display_name: id, filename: `${id}.csv`, tags: [] } as unknown as Resource;
}

beforeEach(() => {
  fetchJSON.mockReset();
  fetchJSON.mockResolvedValue({});
  fetchRaw.mockReset();
  fetchRaw.mockResolvedValue(new Response(null, { status: 204 }));
});

describe("reading a folder's files", () => {
  it("pages until the total is reached", async () => {
    fetchJSON
      .mockResolvedValueOnce({ resources: Array.from({ length: 200 }, (_, i) => res(`r${i}`, "a")), total: 250 })
      .mockResolvedValueOnce({ resources: Array.from({ length: 50 }, (_, i) => res(`s${i}`, "a/b")), total: 250 });
    expect(await filesUnder(root, "a")).toHaveLength(250);
    expect(fetchJSON.mock.calls[1]![0]).toContain("offset=200");
  });

  it("refuses a folder past what one action covers rather than acting on part of it", async () => {
    fetchJSON.mockResolvedValueOnce({ resources: [], total: MAX_FOLDER_FILES + 1 });
    await expect(filesUnder(root, "a")).rejects.toThrow(/more than the 500/);
  });
});

describe("moving", () => {
  it("skips what is already there and refuses a folder into itself", async () => {
    const { outcomes, undo } = await moveMany(
      root,
      { files: [res("x", "dest"), res("y", "other")], folders: ["dest/inner", "a", "top"] },
      "dest",
    );
    expect(outcomes).toEqual([
      { name: "a" },
      { name: "top" },
      { name: "y" },
    ]);
    // dest/inner is already in dest; "a" and "top" nest under it; x is already there.
    expect(undo).toEqual([
      { kind: "folder", from: "dest/a", to: "a" },
      { kind: "folder", from: "dest/top", to: "top" },
      { kind: "file", id: "y", name: "y", path: "other" },
    ]);
  });

  it("refuses a folder dropped into its own subtree", async () => {
    const { outcomes } = await moveMany(root, { files: [], folders: ["a"] }, "a/b");
    expect(outcomes).toEqual([{ name: "a", error: "cannot go inside itself" }]);
  });

  it("puts back newest first and reports a step that could not be undone", async () => {
    fetchJSON.mockImplementation(async (path: string) => {
      if (path === "/folders/move") throw new Error("taken");
      return {};
    });
    const back = await undoMoves(root, [
      { kind: "folder", from: "dest/a", to: "a" },
      { kind: "file", id: "y", name: "y", path: "other" },
    ]);
    expect(back).toEqual([{ name: "y" }, { name: "a", error: "taken" }]);
    expect(summarize(back, "moved")).toBe("1 of 2 moved, 1 refused");
  });
});

describe("deleting", () => {
  it("counts a folder already gone with its last file as deleted", async () => {
    fetchRaw.mockResolvedValue(new Response(JSON.stringify({ error: "no folder at that path" }), { status: 404 }));
    expect(await deleteMany(root, [], ["a"])).toEqual([{ name: "a/" }]);
  });

  it("reports a folder the server kept because a file is still in it", async () => {
    fetchRaw.mockResolvedValue(new Response(JSON.stringify({ error: "the folder still holds files" }), { status: 409 }));
    expect(await deleteMany(root, [], ["a"])).toEqual([{ name: "a/", error: "the folder still holds files" }]);
  });
});
