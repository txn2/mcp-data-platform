import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { useState } from "react";
import { render, screen, cleanup, fireEvent, waitFor, within } from "@testing-library/react";
import { useAuthStore, type UserProfile } from "@/stores/auth";

const uploadResource = vi.hoisted(() => vi.fn());
const updateResource = vi.hoisted(() => vi.fn());
const deleteResource = vi.hoisted(() => vi.fn());
const moveFolder = vi.hoisted(() => vi.fn());

// The page's only data dependencies. The library list is stubbed empty so the
// empty state — the other place the Upload control appears — is what renders.
vi.mock("@/api/resources/hooks", () => ({
  useInfiniteResources: vi.fn(() => ({
    data: { data: [], total: 0 },
    isLoading: false,
    hasNextPage: false,
    isFetchingNextPage: false,
    fetchNextPage: vi.fn(),
  })),
  useUploadResource: vi.fn(() => ({ mutateAsync: uploadResource })),
  useUpdateResource: vi.fn(() => ({ mutateAsync: updateResource })),
  useDeleteResource: vi.fn(() => ({ mutateAsync: deleteResource })),
  useMoveFolder: vi.fn(() => ({ mutateAsync: moveFolder, isPending: false })),
}));

vi.mock("@/api/admin/hooks", () => ({
  usePersonas: vi.fn(() => ({ data: { personas: [] } })),
}));

import { useInfiniteResources } from "@/api/resources/hooks";
import type { Resource } from "@/api/resources/types";
import { ResourcesPage } from "./ResourcesPage";

function signIn(overrides: Partial<UserProfile> = {}) {
  useAuthStore.setState({
    user: {
      user_id: "analyst@example.com",
      email: "analyst@example.com",
      roles: ["dp_analyst"],
      is_admin: false,
      persona: "analyst",
      ...overrides,
    },
  });
}

beforeEach(() => {
  uploadResource.mockReset();
  uploadResource.mockResolvedValue({});
  updateResource.mockReset();
  updateResource.mockResolvedValue({});
  moveFolder.mockReset();
  moveFolder.mockResolvedValue({ from: "", to: "", moved: [] });
  signIn();
});

afterEach(() => {
  cleanup();
  useAuthStore.setState({ user: null });
});

// Radix tabs activate on pointer-down, not on a synthesized click.
function selectTab(name: string) {
  fireEvent.mouseDown(screen.getByRole("tab", { name }), { button: 0 });
}

/**
 * The shell, as far as this page can tell: it holds the location and hands the
 * page a new one whenever the page navigates.
 *
 * The library's location is read out of the route now (#1530), so a harness
 * that rendered the page with a fixed location would leave every tab click and
 * every folder open with no visible effect and would prove nothing about them.
 */
function Shell({ admin, start }: { admin?: boolean; start: string }) {
  const [location, setLocation] = useState(start);
  return (
    <ResourcesPage
      admin={admin}
      location={location}
      onNavigate={(path) => {
        navigations.push(path);
        setLocation(path);
      }}
    />
  );
}

let navigations: string[] = [];

beforeEach(() => {
  navigations = [];
});

function renderPage(opts: { admin?: boolean; start?: string } = {}) {
  const start = opts.start ?? (opts.admin ? "/admin/resources" : "/resources");
  return render(<Shell admin={opts.admin} start={start} />);
}

describe("the Resources page offers Upload only where the caller may add", () => {
  it("offers it on the caller's own library", () => {
    renderPage();
    expect(screen.getByRole("button", { name: "Upload" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Upload Resource" })).toBeTruthy();
    expect(screen.queryByTestId("scope-read-only")).toBeNull();
  });

  it("withholds it on the global library, naming who publishes there instead", () => {
    renderPage();
    selectTab("Global");

    expect(screen.queryByRole("button", { name: "Upload" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Upload Resource" })).toBeNull();
    expect(screen.getByTestId("scope-read-only").textContent).toContain(
      "Published by platform administrators",
    );
    expect(screen.getByTestId("resources-read-only").textContent).toContain(
      "Published by platform administrators",
    );
  });

  it("withholds it on a persona library the caller only belongs to", () => {
    renderPage();
    selectTab("analyst");

    expect(screen.queryByRole("button", { name: "Upload" })).toBeNull();
    expect(screen.getByTestId("scope-read-only").textContent).toContain(
      "Published by the analyst persona's administrators",
    );
  });

  it("offers it on a persona library the caller administers", () => {
    signIn({ roles: ["dp_analyst", "dp_persona-admin:analyst"] });
    renderPage();
    selectTab("analyst");

    expect(screen.getByRole("button", { name: "Upload" })).toBeTruthy();
    expect(screen.queryByTestId("scope-read-only")).toBeNull();
  });

  // A platform admin reading their own portal is a reader, not an operator.
  // The override that makes every library writable belongs to the
  // administrator's section; offering the same Upload on the portal's Global
  // tab put publishing to everyone signed in one click away from browsing.
  it("withholds it from a platform admin on the user page's global library", () => {
    signIn({ is_admin: true });
    renderPage();
    selectTab("Global");

    expect(screen.queryByRole("button", { name: "Upload" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Upload Resource" })).toBeNull();
  });

  it("withholds it from a platform admin on the user page's persona library", () => {
    signIn({ is_admin: true });
    renderPage();
    selectTab("analyst");

    expect(screen.queryByRole("button", { name: "Upload" })).toBeNull();
  });

  it("still offers it to a platform admin on their own library", () => {
    signIn({ is_admin: true });
    renderPage();

    expect(screen.getByRole("button", { name: "Upload" })).toBeTruthy();
  });

  // Withholding a control from somebody who holds the authority has to name
  // where the authority is exercised, or it reads as the platform having lost
  // track of who they are.
  it("tells the platform admin where they do add to the library instead", () => {
    signIn({ is_admin: true });
    renderPage();
    selectTab("Global");

    expect(screen.getByTestId("scope-read-only").textContent).toContain(
      "Add to it from Admin > Resources.",
    );
  });

  it("says no such thing to a reader who has no such authority", () => {
    renderPage();
    selectTab("Global");

    expect(screen.getByTestId("scope-read-only").textContent).not.toContain("Admin > Resources");
  });

  it("keeps every library writable in the administrator's own section", () => {
    signIn({ is_admin: true });
    renderPage({ admin: true });
    selectTab("Global");

    expect(screen.getByRole("button", { name: "Upload" })).toBeTruthy();
    expect(screen.queryByTestId("scope-read-only")).toBeNull();
  });
});

describe("the upload dialog states where the file will land", () => {
  it("names the caller's own library before a file is chosen", () => {
    renderPage();
    fireEvent.click(screen.getByRole("button", { name: "Upload" }));

    const destination = screen.getByTestId("upload-destination");
    expect(destination.textContent).toContain("My Resources");
    expect(destination.textContent).toContain("Only you can see it.");
    // A destination stated only after a file is picked would be stated too late.
    expect(screen.getByText("Choose file (max 100 MB)")).toBeTruthy();
  });

  it("names the persona library when that is the tab in view", () => {
    signIn({ roles: ["dp_analyst", "dp_persona-admin:analyst"] });
    renderPage();
    selectTab("analyst");
    fireEvent.click(screen.getByRole("button", { name: "Upload" }));

    expect(screen.getByTestId("upload-destination").textContent).toContain("analyst persona");
  });

  it("leaves the admin page its scope picker rather than a fixed line", () => {
    signIn({ is_admin: true, roles: ["admin"] });
    renderPage({ admin: true });
    fireEvent.click(screen.getByRole("button", { name: "Upload" }));

    expect(screen.getByLabelText("Scope")).toBeTruthy();
    expect(screen.queryByTestId("upload-destination")).toBeNull();
  });

  // Uploading from inside a folder and having the file appear somewhere else is
  // the thing this defaulting is for (#1530). It is stated and changeable, not
  // silent.
  it("files into the folder the person is standing in", () => {
    renderPage({ start: "/resources/lib/user/data/media-manager" });
    fireEvent.click(screen.getByRole("button", { name: "Upload" }));

    expect((screen.getByLabelText("Folder") as HTMLInputElement).value).toBe(
      "data/media-manager",
    );
  });
});

// fillAndSubmit completes the open dialog with the smallest draft the client
// accepts and sends it, so the assertion is on the request the page actually
// makes rather than on the form state behind it.
async function fillAndSubmit(container: HTMLElement) {
  fireEvent.change(screen.getByLabelText("Display Name"), { target: { value: "Query guide" } });
  fireEvent.change(screen.getByLabelText("Description"), { target: { value: "How we query" } });
  const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement;
  fireEvent.change(fileInput, {
    target: { files: [new File(["# guide"], "guide.md", { type: "text/markdown" })] },
  });
  const dialog = screen.getByRole("dialog");
  fireEvent.click(within(dialog).getByRole("button", { name: "Upload" }));
  await waitFor(() => expect(uploadResource).toHaveBeenCalledTimes(1));
  return uploadResource.mock.calls[0]![0] as FormData;
}

describe("an upload from the user page is filed under the tab it was started from", () => {
  it("sends the caller's own scope from My Resources", async () => {
    const { container } = renderPage();
    fireEvent.click(screen.getByRole("button", { name: "Upload" }));

    const form = await fillAndSubmit(container);
    expect(form.get("scope")).toBe("user");
    expect(form.get("scope_id")).toBe("analyst@example.com");
  });

  it("sends the persona scope from a persona tab the caller administers", async () => {
    signIn({ roles: ["dp_analyst", "dp_persona-admin:analyst"] });
    const { container } = renderPage();
    selectTab("analyst");
    fireEvent.click(screen.getByRole("button", { name: "Upload" }));

    const form = await fillAndSubmit(container);
    expect(form.get("scope")).toBe("persona");
    expect(form.get("scope_id")).toBe("analyst");
  });

  it("sends the folder the person is standing in", async () => {
    const { container } = renderPage({ start: "/resources/lib/user/data/weekly" });
    fireEvent.click(screen.getByRole("button", { name: "Upload" }));

    const form = await fillAndSubmit(container);
    expect(form.get("path")).toBe("data/weekly");
  });
});

const LISTED: Resource = {
  id: "res-1",
  scope: "user",
  scope_id: "analyst@example.com",
  path: "references",
  filename: "seasonal-factors.csv",
  display_name: "Seasonal Factors",
  description: "Monthly demand multipliers.",
  mime_type: "text/csv",
  size_bytes: 64,
  s3_key: "k",
  uri: "mcp://user/analyst@example.com/references/seasonal-factors.csv",
  tags: [],
  uploader_sub: "analyst@example.com",
  uploader_email: "analyst@example.com",
  created_at: "2026-08-03T10:00:00Z",
  updated_at: "2026-08-17T10:00:00Z",
};

function at(path: string, over: Partial<Resource> = {}): Resource {
  return { ...LISTED, id: `res-${path}`, path, display_name: path, ...over };
}

function listing(resources: Resource[]) {
  vi.mocked(useInfiniteResources).mockReturnValue({
    data: { data: resources, total: resources.length },
    isLoading: false,
    hasNextPage: false,
    isFetchingNextPage: false,
    fetchNextPage: vi.fn(),
  } as unknown as ReturnType<typeof useInfiniteResources>);
}

afterEach(() => listing([]));

// A resource opens at an address of its own, which is a navigation -- and a
// navigation unmounts the library, so what the library was showing has to live
// in the address bar or it is gone by the time the reader presses Back (#1470).

describe("opening a resource from the library", () => {
  it("navigates to the resource's own address", () => {
    listing([LISTED]);
    renderPage({ start: "/resources/lib/user/references" });
    navigations.length = 0;

    fireEvent.click(screen.getByText("Seasonal Factors"));
    expect(navigations).toContain("/resources/res-1");
  });

  // A filter typed and clicked through inside the 300ms debounce window has not
  // reached the address bar yet. What is pinned is what is on screen.
  it("pins a search still inside the debounce window", () => {
    listing([LISTED]);
    renderPage({ start: "/resources/lib/user/references" });
    fireEvent.change(screen.getByLabelText("Search resources"), { target: { value: "demand" } });
    navigations.length = 0;

    fireEvent.click(screen.getByText("Seasonal Factors"));
    expect(navigations[0]).toBe("/resources/lib/user/references?q=demand");
  });

  it("pins the view into the entry it leaves, so Back returns to this library", () => {
    listing([LISTED]);
    renderPage({ start: "/resources/lib/global/references" });
    navigations.length = 0;

    fireEvent.click(screen.getByText("Seasonal Factors"));
    expect(navigations).toEqual(["/resources/lib/global/references", "/resources/res-1"]);
  });

  it("keeps the administrator inside their own section", () => {
    listing([LISTED]);
    renderPage({ admin: true, start: "/admin/resources/lib/all/references" });
    navigations.length = 0;

    fireEvent.click(screen.getByText("Seasonal Factors"));
    expect(navigations).toContain("/admin/resources/res-1");
  });
});

describe("browsing the library as a tree", () => {
  const TREE = [
    at("data"),
    at("data/media-manager"),
    at("data/media-manager/shows"),
    at("data/weekly"),
  ];

  it("shows one folder at the root for a library filed several levels deep", () => {
    listing(TREE);
    renderPage();

    const rows = within(screen.getByTestId("folder-list")).getAllByTestId(/^folder-row-/);
    expect(rows.map((r) => r.getAttribute("data-testid"))).toEqual(["folder-row-data"]);
    // The count is everything beneath it, at every depth.
    expect(rows[0]!.textContent).toContain("4");
  });

  it("opens a folder on row click and shows what is directly inside it", () => {
    listing(TREE);
    renderPage();

    fireEvent.click(screen.getByTestId("folder-row-data"));
    expect(navigations).toContain("/resources/lib/user/data");
    expect(screen.getByTestId("folder-row-data/media-manager")).toBeTruthy();
    expect(screen.getByTestId("folder-row-data/weekly")).toBeTruthy();
  });

  it("keeps going down, with each level at its own address", () => {
    listing(TREE);
    renderPage({ start: "/resources/lib/user/data/media-manager" });

    expect(screen.getByTestId("folder-row-data/media-manager/shows")).toBeTruthy();
    fireEvent.click(screen.getByTestId("folder-row-data/media-manager/shows"));
    expect(navigations).toContain("/resources/lib/user/data/media-manager/shows");
  });

  it("narrows the listing to the folder in view", () => {
    listing(TREE);
    renderPage({ start: "/resources/lib/global/data/weekly" });

    expect(useInfiniteResources).toHaveBeenLastCalledWith(
      expect.objectContaining({ scope: "global", path: "data/weekly" }),
    );
  });

  it("walks back up through the breadcrumb", () => {
    listing(TREE);
    renderPage({ start: "/resources/lib/user/data/media-manager/shows" });

    const trail = screen.getAllByLabelText("Folder path")[0]!;
    fireEvent.click(within(trail).getByRole("button", { name: "data" }));
    expect(navigations).toContain("/resources/lib/user/data");
  });

  it("names the library at the head of the trail and does not make it a control", () => {
    listing([]);
    renderPage();
    const trail = screen.getAllByLabelText("Folder path")[0]!;
    expect(trail.textContent).toContain("My Resources");
    expect(within(trail).queryByRole("button")).toBeNull();
  });

  // The administrator's "All Resources" tab spans every library and is the
  // destination of none, so a head read off a move target would call it "My
  // Resources" -- which is a different library, and one of the tabs beside it.
  it("heads the trail with the tab's own name on the unfiltered admin library", () => {
    listing([]);
    renderPage({ admin: true });
    expect(screen.getAllByLabelText("Folder path")[0]!.textContent).toContain("All Resources");
  });
});

describe("searching a library from inside a folder", () => {
  it("searches the whole library rather than the folder in view", async () => {
    listing([at("data/weekly")]);
    renderPage({ start: "/resources/lib/user/data/media-manager?q=demand" });

    // The folder is dropped from the request: a hit elsewhere in the library is
    // the point of searching from inside a folder.
    await waitFor(() =>
      expect(useInfiniteResources).toHaveBeenLastCalledWith(
        expect.objectContaining({ q: "demand", path: undefined }),
      ),
    );
  });

  it("shows each hit with the folder it was found at", () => {
    listing([at("data/weekly")]);
    renderPage({ start: "/resources/lib/user?q=demand" });

    expect(screen.getByTestId("search-hit-path-res-data/weekly").textContent).toBe("data/weekly");
  });

  it("reveals a hit by walking the tree to its folder", () => {
    listing([at("data/weekly")]);
    renderPage({ start: "/resources/lib/user?q=demand" });
    navigations.length = 0;

    fireEvent.click(screen.getByLabelText("Reveal data/weekly in data/weekly"));
    expect(navigations).toContain("/resources/lib/user/data/weekly");
  });
});

describe("acting on several files at once", () => {
  const TWO = [at("data", { id: "a", display_name: "First" }), at("data", { id: "b", display_name: "Second" })];

  function pick(name: string) {
    fireEvent.click(screen.getByLabelText(`Select ${name}`));
  }

  it("says nothing until something is picked", () => {
    listing(TWO);
    renderPage({ start: "/resources/lib/user/data" });
    expect(screen.queryByTestId("selection-bar")).toBeNull();
  });

  it("counts what is picked and offers the three actions", () => {
    listing(TWO);
    renderPage({ start: "/resources/lib/user/data" });
    pick("First");

    const bar = screen.getByTestId("selection-bar");
    expect(bar.textContent).toContain("1 selected");
    for (const action of ["Move", "Tag", "Delete"]) {
      expect(within(bar).getByRole("button", { name: action })).toBeTruthy();
    }
  });

  it("moves every picked file and reports what happened to each", async () => {
    listing(TWO);
    renderPage({ start: "/resources/lib/user/data" });
    pick("First");
    pick("Second");
    fireEvent.click(within(screen.getByTestId("selection-bar")).getByRole("button", { name: "Move" }));

    const dialog = screen.getByRole("dialog");
    fireEvent.change(within(dialog).getByLabelText("Destination folder"), {
      target: { value: "archive" },
    });
    fireEvent.click(within(dialog).getByRole("button", { name: "Move" }));

    await waitFor(() => expect(updateResource).toHaveBeenCalledTimes(2));
    expect(updateResource.mock.calls.map((c) => c[0])).toEqual([
      { id: "a", update: { path: "archive" } },
      { id: "b", update: { path: "archive" } },
    ]);
  });

  // The successes are named as well as the refusals: a report listing only what
  // failed leaves somebody unable to tell whether the rest was touched at all.
  it("leaves a refused file where it was and gives the reason next to it", async () => {
    listing(TWO);
    updateResource.mockImplementation(({ id }: { id: string }) =>
      id === "b" ? Promise.reject(new Error("that name is taken")) : Promise.resolve({}),
    );
    renderPage({ start: "/resources/lib/user/data" });
    pick("First");
    pick("Second");
    fireEvent.click(within(screen.getByTestId("selection-bar")).getByRole("button", { name: "Move" }));

    const dialog = screen.getByRole("dialog");
    fireEvent.change(within(dialog).getByLabelText("Destination folder"), {
      target: { value: "archive" },
    });
    fireEvent.click(within(dialog).getByRole("button", { name: "Move" }));

    const report = await screen.findByTestId("bulk-report");
    expect(report.textContent).toContain("1 of 2 done, 1 refused");
    expect(report.textContent).toContain("that name is taken");
    expect(report.textContent).toContain("First");
  });

  it("refuses a destination that breaks the path rules before sending anything", async () => {
    listing(TWO);
    renderPage({ start: "/resources/lib/user/data" });
    pick("First");
    fireEvent.click(within(screen.getByTestId("selection-bar")).getByRole("button", { name: "Move" }));

    const dialog = screen.getByRole("dialog");
    fireEvent.change(within(dialog).getByLabelText("Destination folder"), {
      target: { value: "Archive" },
    });
    fireEvent.click(within(dialog).getByRole("button", { name: "Move" }));

    await waitFor(() => expect(within(dialog).getByRole("alert")).toBeTruthy());
    expect(updateResource).not.toHaveBeenCalled();
  });
});

function renameFolder(name: string) {
  fireEvent.click(screen.getByLabelText(`Rename or move ${name}`));
}

describe("renaming a folder", () => {
  it("sends one request for the whole subtree", async () => {
    listing([at("data"), at("data/weekly")]);
    renderPage({ start: "/resources/lib/user" });

    renameFolder("data");
    const dialog = screen.getByRole("dialog");
    fireEvent.change(within(dialog).getByLabelText("New path"), { target: { value: "archive" } });
    fireEvent.click(within(dialog).getByRole("button", { name: "Move" }));

    await waitFor(() =>
      expect(moveFolder).toHaveBeenCalledWith({
        scope: "user",
        scope_id: "analyst@example.com",
        from: "data",
        to: "archive",
      }),
    );
  });

  it("refuses to put a folder inside itself", async () => {
    listing([at("data")]);
    renderPage({ start: "/resources/lib/user" });

    renameFolder("data");
    const dialog = screen.getByRole("dialog");
    fireEvent.change(within(dialog).getByLabelText("New path"), { target: { value: "data/old" } });
    fireEvent.click(within(dialog).getByRole("button", { name: "Move" }));

    expect((await screen.findByTestId("folder-move-error")).textContent).toContain(
      "cannot hold it",
    );
    expect(moveFolder).not.toHaveBeenCalled();
  });

  // A folder that has just been renamed no longer exists at the address the
  // person is standing at, so they are taken to where their files went.
  it("does not leave the person looking at a folder that is gone", async () => {
    listing([at("data/weekly")]);
    renderPage({ start: "/resources/lib/user/data" });

    renameFolder("weekly");
    const dialog = screen.getByRole("dialog");
    fireEvent.change(within(dialog).getByLabelText("New path"), { target: { value: "archive" } });
    fireEvent.click(within(dialog).getByRole("button", { name: "Move" }));

    await waitFor(() => expect(moveFolder).toHaveBeenCalled());
    // The reader was standing above the folder that moved, so nothing under
    // them vanished and they stay where they are.
    expect(navigations).not.toContain("/resources/lib/user/archive");
  });

  it("follows the reader down when the folder they are standing in is the one that moved", async () => {
    listing([at("data/weekly")]);
    renderPage({ start: "/resources/lib/user/data/weekly" });

    // The trail is the way to the folder's own actions from inside it: step up
    // one level, then rename it there.
    const trail = screen.getAllByLabelText("Folder path")[0]!;
    fireEvent.click(within(trail).getByRole("button", { name: "data" }));
    renameFolder("weekly");
    const dialog = screen.getByRole("dialog");
    fireEvent.change(within(dialog).getByLabelText("New path"), { target: { value: "archive" } });
    fireEvent.click(within(dialog).getByRole("button", { name: "Move" }));

    await waitFor(() => expect(moveFolder).toHaveBeenCalled());
  });
});

/**
 * A DataTransfer stand-in: jsdom does not implement one, and fireEvent.drop
 * hands the handler whatever is passed here.
 */
function transfer(entries: Record<string, string>): DataTransfer {
  return {
    setData: (type: string, value: string) => {
      entries[type] = value;
    },
    getData: (type: string) => entries[type] ?? "",
    effectAllowed: "none",
  } as unknown as DataTransfer;
}

describe("dragging things onto a folder", () => {
  const TREE = [
    at("data", { id: "a", display_name: "First" }),
    at("data/weekly"),
    at("archive"),
  ];

  it("moves a dragged file into the folder it was dropped on", async () => {
    listing(TREE);
    // Standing in the folder that holds both the file and the subfolder it is
    // dragged into.
    renderPage({ start: "/resources/lib/user/data" });

    const dataTransfer = transfer({});
    fireEvent.dragStart(screen.getByTestId("resource-row-a"), { dataTransfer });
    fireEvent.drop(screen.getByTestId("folder-row-data/weekly"), { dataTransfer });

    // The move still asks: dragging is easy to do by accident and this one
    // rewrites an address. The destination is already the folder it was
    // dropped on.
    const dialog = await screen.findByRole("dialog");
    expect((within(dialog).getByLabelText("Destination folder") as HTMLInputElement).value).toBe(
      "data/weekly",
    );
    fireEvent.click(within(dialog).getByRole("button", { name: "Move" }));
    await waitFor(() =>
      expect(updateResource).toHaveBeenCalledWith({
        id: "a",
        update: { path: "data/weekly" },
      }),
    );
  });

  it("nests a dragged folder inside the folder it was dropped on", async () => {
    listing(TREE);
    renderPage({ start: "/resources/lib/user" });

    const dataTransfer = transfer({});
    fireEvent.dragStart(screen.getByTestId("folder-row-archive"), { dataTransfer });
    fireEvent.drop(screen.getByTestId("folder-row-data"), { dataTransfer });

    const dialog = await screen.findByRole("dialog");
    expect((within(dialog).getByLabelText("New path") as HTMLInputElement).value).toBe(
      "data/archive",
    );
    fireEvent.click(within(dialog).getByRole("button", { name: "Move" }));
    await waitFor(() =>
      expect(moveFolder).toHaveBeenCalledWith(
        expect.objectContaining({ from: "archive", to: "data/archive" }),
      ),
    );
  });

  it("does nothing when a folder is dropped on itself", () => {
    listing(TREE);
    renderPage({ start: "/resources/lib/user" });

    const dataTransfer = transfer({});
    fireEvent.dragStart(screen.getByTestId("folder-row-data"), { dataTransfer });
    fireEvent.drop(screen.getByTestId("folder-row-data"), { dataTransfer });

    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("offers no drop target on a library the caller may not write", () => {
    listing(TREE);
    renderPage({ start: "/resources/lib/global" });

    const dataTransfer = transfer({ "application/x-mcp-resources": "res-archive" });
    fireEvent.drop(screen.getByTestId("folder-row-data"), { dataTransfer });
    expect(screen.queryByRole("dialog")).toBeNull();
  });
});

describe("the library's view lives in its address", () => {
  it("writes the scope tab into the address", () => {
    listing([]);
    renderPage();
    navigations.length = 0;

    selectTab("Global");
    expect(navigations).toContain("/resources/lib/global");
  });

  it("opens on the library, the folder and the filters its address names", () => {
    listing([]);
    renderPage({ start: "/resources/lib/global/data?q=demand&tag=q3" });

    expect(screen.getByRole("tab", { name: "Global" }).getAttribute("aria-selected")).toBe("true");
    expect((screen.getByLabelText("Search resources") as HTMLInputElement).value).toBe("demand");
    expect(useInfiniteResources).toHaveBeenCalledWith(
      expect.objectContaining({ scope: "global", q: "demand", tag: "q3" }),
    );
  });
});

// Tags are stored, indexed, and filterable on the server, and were filterable
// everywhere except on the page that shows them (#1471).

const TAGGED: Resource = {
  ...LISTED,
  id: "res-2",
  path: "data",
  display_name: "Q3 Rates",
  tags: ["q3", "finance"],
};

async function chooseTag(name: string) {
  fireEvent.click(screen.getByLabelText("Filter by tag"));
  fireEvent.click(await screen.findByRole("option", { name }));
}

describe("the library's tag filter", () => {
  it("offers the tags the resources in view carry", async () => {
    listing([LISTED, TAGGED]);
    renderPage();

    fireEvent.click(screen.getByLabelText("Filter by tag"));
    expect(await screen.findByRole("option", { name: "finance" })).toBeTruthy();
    expect(screen.getByRole("option", { name: "q3" })).toBeTruthy();
  });

  it("narrows the request to the tag chosen", async () => {
    listing([LISTED, TAGGED]);
    renderPage();
    await chooseTag("q3");

    expect(useInfiniteResources).toHaveBeenLastCalledWith(
      expect.objectContaining({ tag: "q3", scope: "user" }),
    );
  });

  // A tag that matched nothing is a filter that missed, not a library nobody
  // has uploaded to; the two send the reader to different places.
  it("reads a tag that matched nothing as a filter, not as an empty library", () => {
    listing([]);
    renderPage({ start: "/resources/lib/user?tag=q3" });

    expect(screen.getByTestId("resources-empty").textContent).toContain("No resources match");
  });

  it("goes inert on a library nobody has tagged", () => {
    listing([LISTED]);
    renderPage();

    expect((screen.getByLabelText("Filter by tag") as HTMLButtonElement).disabled).toBe(true);
  });

  it("carries the tag across a scope tab change", async () => {
    listing([LISTED, TAGGED]);
    renderPage();
    await chooseTag("q3");
    selectTab("Global");

    expect(useInfiniteResources).toHaveBeenLastCalledWith(
      expect.objectContaining({ scope: "global" }),
    );
  });
});
