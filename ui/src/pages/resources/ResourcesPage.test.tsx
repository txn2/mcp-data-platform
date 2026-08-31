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
  // The recently-updated strip's own request. Empty here, which is what leaves
  // it off the page: every case below is about the library list beneath it.
  useResources: vi.fn(() => ({ data: { resources: [], total: 0 }, isLoading: false })),
  // The library's folders, which the tree is drawn from (#1555).
  useFacets: vi.fn(() => ({ data: { folders: [], tags: [] }, isLoading: false })),
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

import { useFacets, useInfiniteResources, useResources } from "@/api/resources/hooks";
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

// chooseOption drives a Radix listbox: jsdom has no PointerEvent, so the
// trigger's pointerdown handler never fires and it is opened from the keyboard.
function chooseOption(name: string, option: string): void {
  fireEvent.keyDown(screen.getByRole("combobox", { name }), { key: "Enter" });
  fireEvent.click(screen.getByRole("option", { name: option }));
}

// The library in view is one listbox now rather than a strip of tabs (#1553).
function selectLibrary(name: string) {
  chooseOption("Library", name);
}

// A folder control is a listbox too: an existing folder is chosen from the
// list, and one that does not exist yet is typed after the new-folder entry
// swaps the control over to a text field.
function typeFolder(scope: HTMLElement, label: string, value: string) {
  fireEvent.keyDown(within(scope).getByRole("combobox", { name: label }), { key: "Enter" });
  fireEvent.click(screen.getByRole("option", { name: "New folder..." }));
  fireEvent.change(within(scope).getByLabelText(label), { target: { value } });
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
    selectLibrary("Global");

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
    selectLibrary("analyst");

    expect(screen.queryByRole("button", { name: "Upload" })).toBeNull();
    expect(screen.getByTestId("scope-read-only").textContent).toContain(
      "Published by the analyst persona's administrators",
    );
  });

  it("offers it on a persona library the caller administers", () => {
    signIn({ roles: ["dp_analyst", "dp_persona-admin:analyst"] });
    renderPage();
    selectLibrary("analyst");

    expect(screen.getByRole("button", { name: "Upload" })).toBeTruthy();
    expect(screen.queryByTestId("scope-read-only")).toBeNull();
  });

  // The server grants a platform admin every library whatever route the request
  // arrived on, so the page offers it on every tab the administrator is looking
  // at. Withholding it here left them reading a global library they hold the
  // authority to publish to, on a page that would not let them (#1527).
  it("offers it to a platform admin on the user page's global library", () => {
    signIn({ is_admin: true });
    renderPage();
    selectLibrary("Global");

    expect(screen.getByRole("button", { name: "Upload" })).toBeTruthy();
    expect(screen.queryByTestId("scope-read-only")).toBeNull();
  });

  it("offers it to a platform admin on the user page's persona library", () => {
    signIn({ is_admin: true });
    renderPage();
    selectLibrary("analyst");

    expect(screen.getByRole("button", { name: "Upload" })).toBeTruthy();
  });

  it("still offers it to a platform admin on their own library", () => {
    signIn({ is_admin: true });
    renderPage();

    expect(screen.getByRole("button", { name: "Upload" })).toBeTruthy();
  });

  it("keeps every library writable in the administrator's own section", () => {
    signIn({ is_admin: true });
    renderPage({ admin: true });
    selectLibrary("Global");

    expect(screen.getByRole("button", { name: "Upload" })).toBeTruthy();
    expect(screen.queryByTestId("scope-read-only")).toBeNull();
  });
});

describe("the upload dialog states where the file will land", () => {
  it("names the caller's own library before a file is chosen", () => {
    renderPage();
    selectLibrary("Mine");
    fireEvent.click(screen.getByRole("button", { name: "Upload" }));

    const destination = screen.getByTestId("upload-destination");
    expect(destination.textContent).toContain("My Resources");
    expect(destination.textContent).toContain("Only you can see it.");
    // A destination stated only after a file is picked would be stated too late.
    expect(screen.getByText("Choose file (max 100 MB)")).toBeTruthy();
  });

  // The All view names no library, so the dialog asks. Without this the Upload
  // control on the view every page opens on would either be missing for an
  // ordinary reader or file silently (#1553).
  it("asks which library on the All view, defaulting to the caller's own", () => {
    renderPage();
    fireEvent.click(screen.getByRole("button", { name: "Upload" }));

    const picker = screen.getByTestId("upload-destination-picker");
    expect(picker.textContent).toContain("My Resources");
    expect(picker.textContent).toContain("Only you can see it.");
    expect(screen.queryByTestId("upload-destination")).toBeNull();
  });

  it("offers a persona the caller administers as a destination on the All view", () => {
    signIn({ roles: ["dp_analyst", "dp_persona-admin:analyst"] });
    renderPage();
    fireEvent.click(screen.getByRole("button", { name: "Upload" }));

    fireEvent.keyDown(screen.getByRole("combobox", { name: "Destination" }), { key: "Enter" });
    expect(screen.getByRole("option", { name: "My Resources" })).toBeTruthy();
    expect(screen.getByRole("option", { name: "analyst persona" })).toBeTruthy();
  });

  it("names the persona library when that is the tab in view", () => {
    signIn({ roles: ["dp_analyst", "dp_persona-admin:analyst"] });
    renderPage();
    selectLibrary("analyst");
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

    // The folder is a listbox now, so the destination is what its trigger says
    // rather than an input's value (#1553).
    expect(screen.getByRole("combobox", { name: "Folder" }).textContent).toContain(
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
    selectLibrary("Mine");
    fireEvent.click(screen.getByRole("button", { name: "Upload" }));

    const form = await fillAndSubmit(container);
    expect(form.get("scope")).toBe("user");
    expect(form.get("scope_id")).toBe("analyst@example.com");
  });

  it("sends the persona scope from a persona tab the caller administers", async () => {
    signIn({ roles: ["dp_analyst", "dp_persona-admin:analyst"] });
    const { container } = renderPage();
    selectLibrary("analyst");
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

  // The tab is the destination for an administrator too. A file they meant for
  // everyone signed in silently landing in their own library would be the worse
  // half of the defect the Upload control's absence hid (#1527).
  it("sends the global scope when a platform admin uploads from the Global tab", async () => {
    signIn({ is_admin: true });
    const { container } = renderPage();
    selectLibrary("Global");
    fireEvent.click(screen.getByRole("button", { name: "Upload" }));

    const form = await fillAndSubmit(container);
    expect(form.get("scope")).toBe("global");
    expect(form.get("scope_id")).toBeNull();
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

// listing seeds both halves of what a library shows: the files the listing
// returns, and the folders the server reports for them. They are seeded
// together because the server derives the second from the first (#1555), so a
// test that set only one would describe a library that cannot exist.
function listing(resources: Resource[]) {
  const counts = new Map<string, number>();
  for (const r of resources) {
    const parts = r.path.split("/").filter(Boolean);
    for (let i = 0; i < parts.length; i++) {
      const prefix = parts.slice(0, i + 1).join("/");
      counts.set(prefix, (counts.get(prefix) ?? 0) + 1);
    }
  }
  const tags = [...new Set(resources.flatMap((r) => r.tags ?? []))].sort();
  vi.mocked(useFacets).mockReturnValue({
    data: { folders: [...counts].map(([path, count]) => ({ path, count })), tags },
    isLoading: false,
  } as unknown as ReturnType<typeof useFacets>);

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
    expect(navigations).toContain("/resources/lib/all/data");
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

    expect(useInfiniteResources).toHaveBeenLastCalledWith(expect.objectContaining({ scope: "global", path: "data/weekly" }), true);
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
    renderPage({ start: "/resources/lib/user" });
    const trail = screen.getAllByLabelText("Folder path")[0]!;
    expect(trail.textContent).toContain("Mine");
    expect(within(trail).queryByRole("button")).toBeNull();
  });

  // The All view spans every library and is the destination of none, so a head
  // read off a move target would call it "My Resources" -- which is a different
  // library, and another entry in the picker beside it.
  it("heads the trail with the picker's own name on the unnarrowed library", () => {
    listing([]);
    renderPage({ admin: true });
    expect(screen.getAllByLabelText("Folder path")[0]!.textContent).toContain("All");
  });
});

describe("searching a library from inside a folder", () => {
  it("searches the whole library rather than the folder in view", async () => {
    listing([at("data/weekly")]);
    renderPage({ start: "/resources/lib/user/data/media-manager?q=demand" });

    // The folder is dropped from the request: a hit elsewhere in the library is
    // the point of searching from inside a folder.
    await waitFor(() =>
      expect(useInfiniteResources).toHaveBeenLastCalledWith(expect.objectContaining({ q: "demand", path: undefined }), true),
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
    typeFolder(dialog, "Destination folder", "archive");
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
    typeFolder(dialog, "Destination folder", "archive");
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
    typeFolder(dialog, "Destination folder", "Archive");
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
    fireEvent.dragStart(screen.getByTestId("resource-tile-a"), { dataTransfer });
    fireEvent.drop(screen.getByTestId("folder-row-data/weekly"), { dataTransfer });

    // The move still asks: dragging is easy to do by accident and this one
    // rewrites an address. The destination is already the folder it was
    // dropped on.
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByRole("combobox", { name: "Destination folder" }).textContent).toContain(
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

    selectLibrary("Global");
    expect(navigations).toContain("/resources/lib/global");
  });

  it("opens on the library, the folder and the filters its address names", () => {
    listing([]);
    renderPage({ start: "/resources/lib/global/data?q=demand&tag=q3" });

    expect(screen.getByRole("combobox", { name: "Library" }).textContent).toContain("Global");
    expect((screen.getByLabelText("Search resources") as HTMLInputElement).value).toBe("demand");
    expect(useInfiniteResources).toHaveBeenCalledWith(expect.objectContaining({ scope: "global", q: "demand", tag: "q3" }), true);
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

    // The All view sends no scope: the server answers it with every library
    // the caller may read (#1553).
    expect(useInfiniteResources).toHaveBeenLastCalledWith(expect.objectContaining({ tag: "q3" }), true);
    const calls = vi.mocked(useInfiniteResources).mock.calls;
    expect(calls[calls.length - 1]![0]!.scope).toBeUndefined();
  });

  // A tag that matched nothing is a filter that missed, not a library nobody
  // has uploaded to; the two send the reader to different places. A tag spans
  // the library the way a search does, so the refusal says so (#1555).
  it("reads a tag that matched nothing as a filter, not as an empty library", () => {
    listing([]);
    renderPage({ start: "/resources/lib/user?tag=q3" });

    const empty = screen.getByTestId("resources-empty").textContent ?? "";
    expect(empty).toContain("Nothing here matches");
    expect(empty).toContain("The whole library was looked through");
  });

  it("goes inert on a library nobody has tagged", () => {
    listing([LISTED]);
    renderPage();

    expect((screen.getByLabelText("Filter by tag") as HTMLButtonElement).disabled).toBe(true);
  });

  // Opening another library is a place, not a narrowing of the one in hand, so
  // it drops the filters and lands at that library's root. The test that stood
  // here was named for carrying the tag across and only ever asserted the
  // scope; the tag has always been cleared (see setActiveTab).
  it("drops the tag when another library is opened, and lands at its root", async () => {
    listing([LISTED, TAGGED]);
    renderPage();
    await chooseTag("q3");
    selectLibrary("Global");

    expect(useFacets).toHaveBeenLastCalledWith({ scope: "global" });
    // A root lists no files, so nothing is paged there (#1555).
    const calls = vi.mocked(useInfiniteResources).mock.calls;
    const last = calls[calls.length - 1]!;
    expect(last[0]).toEqual(expect.objectContaining({ scope: "global", tag: undefined }));
    expect(last[1]).toBe(false);
  });

  // A curator asking "what has nothing read it" is asking the whole library, and
  // it means nothing applied to a list of folder names. Like a tag, it was a
  // control on screen with no listing behind it until this (#1555).
  it("lists the library when an ordering other than the default is chosen at a root", async () => {
    listing([LISTED, TAGGED]);
    renderPage({ admin: true, start: "/admin/resources?sort=last_read" });

    const calls = vi.mocked(useInfiniteResources).mock.calls;
    const last = calls[calls.length - 1]!;
    expect(last[0]).toEqual(expect.objectContaining({ sort: "last_read", path: undefined }));
    expect(last[1]).toBe(true);
  });

  // The default ordering is what a tree is shown under, so it does not turn the
  // root into a list.
  it("leaves the tree alone under the default ordering", () => {
    listing([at("data")]);
    renderPage({ admin: true });

    expect(screen.getByTestId("folder-list")).toBeTruthy();
    const calls = vi.mocked(useInfiniteResources).mock.calls;
    expect(calls[calls.length - 1]![1]).toBe(false);
  });

  // A tag spans the library, so at a root it replaces the tree rather than
  // narrowing a level of it: without this the control was on screen and the
  // listing behind it never ran (#1555).
  it("lists the library's tagged files when a tag is chosen at a root", async () => {
    listing([LISTED, TAGGED]);
    renderPage();
    await chooseTag("q3");

    const calls = vi.mocked(useInfiniteResources).mock.calls;
    const last = calls[calls.length - 1]!;
    expect(last[0]).toEqual(expect.objectContaining({ tag: "q3", path: undefined }));
    expect(last[1]).toBe(true);
  });
});

// --- The library's layout, its recents, and its folder picker (#1553) ---

function recents(resources: Resource[]) {
  vi.mocked(useResources).mockReturnValue({
    data: { resources, total: resources.length },
    isLoading: false,
  } as unknown as ReturnType<typeof useResources>);
}

const IMAGE: Resource = {
  ...LISTED,
  id: "res-img",
  path: "visual",
  filename: "logo.png",
  display_name: "Logo",
  mime_type: "image/png",
};

describe("the library is drawn the way the reader asked", () => {
  it("draws tiles by default, one for every file whatever its type", () => {
    // The mixed folder is the case: it used to fall to rows because one file in
    // it was not an image, so nothing in it got a thumbnail. A root lists no
    // files (#1555), so this stands in the folder that holds them.
    listing([{ ...LISTED, path: "mixed" }, { ...IMAGE, path: "mixed" }]);
    renderPage({ start: "/resources/lib/user/mixed" });

    expect(screen.getByTestId("resource-tile-res-1")).toBeTruthy();
    expect(screen.getByTestId("resource-tile-res-img")).toBeTruthy();
    expect(screen.queryByTestId("resource-row-res-1")).toBeNull();
  });

  // The tile is the resource's stored capture, not the file (#1554): a PNG a
  // portal tab rendered, served from the thumbnail route with the moment it was
  // taken on it so a re-capture is a different URL. A resource with no capture
  // yet draws its content-type icon and requests nothing.
  it("draws a captured tile from the thumbnail route and nothing for an uncaptured file", () => {
    listing([
      { ...LISTED, path: "mixed" },
      {
        ...IMAGE,
        path: "mixed",
        thumbnail_s3_key: "resources/res-img/.thumbnail.png",
        thumbnail_captured_at: "2026-08-30T10:00:00Z",
      },
    ]);
    const { container } = renderPage({ start: "/resources/lib/user/mixed" });

    const sources = [...container.querySelectorAll("img")].map((i) => i.getAttribute("src"));
    expect(sources.some((src) => src?.includes("res-img/thumbnail"))).toBe(true);
    // The capture's moment is on the URL, so a re-capture is not the same one.
    expect(sources.some((src) => src?.includes("c=2026-08-30"))).toBe(true);
    // The uncaptured file issues no request at all.
    expect(sources.some((src) => src?.includes("res-1"))).toBe(false);
  });

  // The choice persists across a reload; that half is the storage helper's,
  // which this environment has no localStorage to exercise (listView.test.ts).
  it("switches to rows on the reader's word", () => {
    listing([{ ...LISTED, path: "mixed" }]);
    renderPage({ start: "/resources/lib/user/mixed" });

    fireEvent.click(screen.getByRole("button", { name: "Table view" }));
    expect(screen.getByTestId("resource-row-res-1")).toBeTruthy();
    expect(screen.queryByTestId("resource-tile-res-1")).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: "Grid view" }));
    expect(screen.getByTestId("resource-tile-res-1")).toBeTruthy();
  });
});

describe("the recently updated strip", () => {
  it("heads the library with the files that changed last", () => {
    listing([at("data")]);
    recents([{ ...LISTED, path: "references" }]);
    renderPage();

    const strip = screen.getByTestId("recent-resources");
    expect(strip.textContent).toContain("Seasonal Factors");
    expect(strip.textContent).toContain("references");
  });

  it("asks for the library in view, ten of them, newest first", () => {
    listing([]);
    recents([LISTED]);
    renderPage();
    selectLibrary("Global");

    expect(useResources).toHaveBeenLastCalledWith({
      scope: "global",
      sort: "updated",
      limit: 10,
    });
  });

  // Inside a folder and under a filter the view is already an answer to "what
  // here is relevant", and a differently ordered second answer above it
  // competes with the one that was asked for.
  it("is left off inside a folder", () => {
    listing([at("data/weekly")]);
    recents([LISTED]);
    renderPage({ start: "/resources/lib/user/data" });

    expect(screen.queryByTestId("recent-resources")).toBeNull();
  });

  it("is left off while a search or a tag filter is narrowing the library", () => {
    listing([LISTED]);
    recents([LISTED]);
    renderPage({ start: "/resources/lib/user?q=demand" });
    expect(screen.queryByTestId("recent-resources")).toBeNull();

    cleanup();
    renderPage({ start: "/resources/lib/user?tag=q3" });
    expect(screen.queryByTestId("recent-resources")).toBeNull();
  });

  it("says nothing about a library with nothing in it", () => {
    listing([]);
    recents([]);
    renderPage();

    expect(screen.queryByTestId("recent-resources")).toBeNull();
  });
});

describe("choosing the folder an upload lands in", () => {
  it("offers the folders the library already has, and the seeds", async () => {
    listing([at("data/media-manager"), at("references")]);
    renderPage({ start: "/resources/lib/user" });
    fireEvent.click(screen.getByRole("button", { name: "Upload" }));

    const dialog = screen.getByRole("dialog");
    fireEvent.keyDown(within(dialog).getByRole("combobox", { name: "Folder" }), { key: "Enter" });

    expect(await screen.findByRole("option", { name: "data/media-manager" })).toBeTruthy();
    expect(screen.getByRole("option", { name: "references" })).toBeTruthy();
    // A seed the library has not used yet is still a reasonable place to start.
    expect(screen.getByRole("option", { name: "playbooks" })).toBeTruthy();
  });

  it("files into a folder chosen from that list", async () => {
    listing([at("data/media-manager")]);
    const { container } = renderPage({ start: "/resources/lib/user" });
    fireEvent.click(screen.getByRole("button", { name: "Upload" }));

    const dialog = screen.getByRole("dialog");
    fireEvent.keyDown(within(dialog).getByRole("combobox", { name: "Folder" }), { key: "Enter" });
    fireEvent.click(await screen.findByRole("option", { name: "data/media-manager" }));

    const form = await fillAndSubmit(container);
    expect(form.get("path")).toBe("data/media-manager");
  });

  // A folder is created by filing something into it, so a folder that does not
  // exist yet has to stay typeable -- it is the ordering that changed.
  it("files into a folder that does not exist yet", async () => {
    listing([at("references")]);
    const { container } = renderPage({ start: "/resources/lib/user" });
    fireEvent.click(screen.getByRole("button", { name: "Upload" }));

    const dialog = screen.getByRole("dialog");
    typeFolder(dialog, "Folder", "data/new-thing");

    const form = await fillAndSubmit(container);
    expect(form.get("path")).toBe("data/new-thing");
  });

  it("leads back to the list after the new-folder field is opened", async () => {
    listing([at("references")]);
    renderPage({ start: "/resources/lib/user" });
    fireEvent.click(screen.getByRole("button", { name: "Upload" }));

    const dialog = screen.getByRole("dialog");
    typeFolder(dialog, "Folder", "data/new-thing");
    fireEvent.click(within(dialog).getByRole("button", { name: "Choose an existing folder" }));

    expect(within(dialog).getByRole("combobox", { name: "Folder" })).toBeTruthy();
  });
});

describe("renaming a folder names one library", () => {
  it("is not offered on the view that spans several", () => {
    listing([at("data")]);
    renderPage();

    expect(screen.queryByLabelText("Rename or move data")).toBeNull();
  });

  it("is offered once the picker names one the caller may write", () => {
    listing([at("data")]);
    renderPage({ start: "/resources/lib/user" });

    expect(screen.getByLabelText("Rename or move data")).toBeTruthy();
  });
});
