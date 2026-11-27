import { render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("@/api/resources/hooks", () => ({
  useFolderLevel: vi.fn(),
  useExternalFolders: vi.fn(() => ({ data: undefined })),
  usePeople: vi.fn(() => ({ data: undefined })),
}));

import { useFolderLevel } from "@/api/resources/hooks";
import type { Folder } from "@/api/resources/types";
import type { ResourceRoot } from "../scopes";
import { FolderTree } from "./FolderTree";
import { PROGRESSIVE_STEP } from "./progressive";

const root: ResourceRoot = {
  key: "global",
  label: "Global",
  target: { scope: "global" } as ResourceRoot["target"],
  params: { scope: "global" },
};

/** One level per parent, as the facets route answers it (#2837). */
const levels: Record<string, Folder[]> = {
  "": [{ path: "mail", count: 10_000, subfolders: 1 }],
  mail: [
    { path: "mail", count: 10_000, subfolders: 1 },
    { path: "mail/2026-01-01", count: 10_000, subfolders: 5000 },
  ],
  "mail/2026-01-01": [
    { path: "mail/2026-01-01", count: 10_000, subfolders: 5000 },
    ...Array.from({ length: 5000 }, (_, i) => ({ path: `mail/2026-01-01/m${String(i).padStart(4, "0")}`, count: 2, subfolders: 0 })),
  ],
};

describe("the folder tree over a large library (#2837)", () => {
  beforeEach(() => {
    vi.stubGlobal(
      "IntersectionObserver",
      class {
        observe() {}
        disconnect() {}
      },
    );
    vi.mocked(useFolderLevel).mockImplementation(((_p: unknown, parent: string) => ({
      data: { folders: levels[parent] ?? [], tags: [] },
    })) as unknown as typeof useFolderLevel);
  });
  afterEach(() => vi.unstubAllGlobals());

  it("reads one level per open folder and draws a folder of 5,000 folders a step at a time", () => {
    render(
      <FolderTree
        roots={[root]}
        people={false}
        ownIDs={[]}
        current={{ root: "global", path: "mail/2026-01-01/m0000" }}
        onOpen={() => {}}
        drop={{ accepts: () => false, onDrop: () => {} }}
      />,
    );
    const asked = vi.mocked(useFolderLevel).mock.calls.map((c) => c[1]);
    expect(new Set(asked)).toEqual(new Set(["", "mail", "mail/2026-01-01"]));

    const day = screen.getByTestId("tree-node-global:mail/2026-01-01");
    expect(day.getAttribute("aria-expanded")).toBe("true");
    const messages = screen.getAllByRole("treeitem").filter((n) => n.dataset.nodeId?.startsWith("global|mail/2026-01-01/"));
    expect(messages).toHaveLength(PROGRESSIVE_STEP);
    expect(messages[0]!.getAttribute("aria-expanded")).toBeNull();
    expect(screen.getByTestId("tree-more")).toBeTruthy();
  });
});
