import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { useState, type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, cleanup, fireEvent, waitFor, within } from "@testing-library/react";
import { useAuthStore, type UserProfile } from "@/stores/auth";
import { useThemeStore } from "@/stores/theme";

// Every write the file manager makes is a request through the resource client,
// recorded here so a case can say what was sent (#1872).
const fetchJSON = vi.hoisted(() => vi.fn());
const fetchRaw = vi.hoisted(() => vi.fn());
vi.mock("@/api/resources/client", () => ({
  resourceFetch: fetchJSON,
  resourceFetchRaw: fetchRaw,
  BASE_URL: "/api/v1/resources",
}));

vi.mock("@/api/resources/hooks", () => ({
  useResources: vi.fn(() => ({ data: { resources: [], total: 0 }, isLoading: false })),
  useFacets: vi.fn(),
  useInfiniteResources: vi.fn(),
  usePeople: vi.fn(),
  useUploadResource: vi.fn(() => ({ mutateAsync: vi.fn() })),
  useInvalidateResources: vi.fn(() => async () => {}),
  useUpdateResource: vi.fn(() => ({ mutateAsync: vi.fn() })),
  useDeleteResource: vi.fn(() => ({ mutateAsync: vi.fn() })),
}));

vi.mock("@/api/admin/hooks", () => ({
  usePersonas: vi.fn(() => ({ data: { personas: [{ name: "analyst" }, { name: "ops" }] } })),
}));

import { useFacets, useInfiniteResources, usePeople } from "@/api/resources/hooks";
import type { Resource } from "@/api/resources/types";
import { ResourcesPage } from "./ResourcesPage";

function signIn(overrides: Partial<UserProfile> = {}) {
  useAuthStore.setState({
    user: {
      user_id: "sub-me",
      email: "me@example.com",
      roles: ["dp_analyst"],
      is_admin: false,
      persona: "analyst",
      ...overrides,
    },
  });
}

const BASE: Resource = {
  id: "res-1",
  scope: "user",
  scope_id: "sub-me",
  path: "data",
  filename: "orders.csv",
  display_name: "orders.csv",
  description: "Weekly orders.",
  mime_type: "text/csv",
  size_bytes: 2048,
  s3_key: "k",
  uri: "mcp://user/sub-me/data/orders.csv",
  tags: ["orders"],
  uploader_sub: "sub-me",
  uploader_email: "me@example.com",
  created_at: "2026-08-03T10:00:00Z",
  updated_at: "2026-08-17T10:00:00Z",
};

function file(id: string, path: string, name: string, over: Partial<Resource> = {}): Resource {
  return { ...BASE, id, path, filename: name, display_name: name, uri: `mcp://user/sub-me/${path}/${name}`, ...over };
}

/** What each top-level folder holds, keyed by the scope a request narrows by. */
let held: Record<string, Resource[]> = {};
/** Folders stored with nothing in them. */
let emptyFolders: Record<string, string[]> = {};
/** The params of every listing request, newest last. */
let listings: Record<string, unknown>[] = [];

function scopeKey(params?: { scope?: string; scope_id?: string }): string {
  return params?.scope === "persona" ? (params.scope_id ?? "") : (params?.scope ?? "");
}

function facetsFor(resources: Resource[], empty: string[]) {
  const counts = new Map<string, number>();
  const add = (path: string, n: number) => {
    const parts = path.split("/");
    for (let i = 1; i <= parts.length; i++) {
      const p = parts.slice(0, i).join("/");
      counts.set(p, (counts.get(p) ?? 0) + n);
    }
  };
  for (const r of resources) add(r.path, 1);
  for (const p of empty) add(p, 0);
  return [...counts].map(([path, count]) => ({ path, count, updated_at: "2026-08-17T10:00:00Z" }));
}

function seed(byScope: Record<string, Resource[]>, empty: Record<string, string[]> = {}) {
  held = byScope;
  emptyFolders = empty;
  vi.mocked(useFacets).mockImplementation(((params?: { scope?: string; scope_id?: string }) => ({
    data: { folders: facetsFor(held[scopeKey(params)] ?? [], emptyFolders[scopeKey(params)] ?? []), tags: [] },
    isLoading: false,
  })) as unknown as typeof useFacets);
  vi.mocked(useInfiniteResources).mockImplementation(((params?: Record<string, unknown>, enabled = true) => {
    if (enabled) listings.push(params ?? {});
    const rows = (held[scopeKey(params as { scope?: string })] ?? []).filter((r) => {
      if (params?.q) return r.display_name.includes(params.q as string);
      if (params?.tag) return r.tags.includes(params.tag as string);
      if (!params?.path) return false;
      return params.direct ? r.path === params.path : r.path.startsWith(params.path as string);
    });
    return {
      data: enabled ? { data: rows, total: rows.length } : undefined,
      isLoading: false,
      hasNextPage: false,
      isFetchingNextPage: false,
      fetchNextPage: vi.fn(),
    };
  }) as unknown as typeof useInfiniteResources);
}

let navigations: string[] = [];

function last<T>(xs: T[]): T | undefined {
  return xs[xs.length - 1];
}

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

function wrap(children: ReactNode) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
}

function renderPage(start = "/resources/lib/user/data", admin = false) {
  return render(wrap(<Shell admin={admin} start={start} />));
}

const row = (key: string) => screen.getByTestId(`row-${key}`);
const node = (root: string, path: string) => screen.getByTestId(`tree-node-${root}:${path}`);

beforeEach(() => {
  navigations = [];
  listings = [];
  fetchJSON.mockReset();
  fetchJSON.mockResolvedValue({});
  fetchRaw.mockReset();
  fetchRaw.mockResolvedValue(new Response(null, { status: 204 }));
  vi.mocked(usePeople).mockReturnValue({ data: undefined } as unknown as ReturnType<typeof usePeople>);
  signIn();
  seed({
    user: [
      file("f-orders", "data", "orders.csv", { size_bytes: 2048 }),
      file("f-regions", "data", "regions.csv", { size_bytes: 1024, tags: ["reference"] }),
      file("f-w38", "data/weekly", "w38.csv"),
      file("f-plan", "drafts", "q3-plan.md", { mime_type: "text/markdown" }),
    ],
    global: [file("g-brand", "brand", "badge.png", { scope: "global", scope_id: "" })],
  });
});

afterEach(() => {
  cleanup();
  useAuthStore.setState({ user: null });
  useThemeStore.setState({ theme: "system" });
  try {
    localStorage.clear();
  } catch {
    // no storage in this environment
  }
});

describe("the folder tree", () => {
  it("lists My Resources, Global and each readable persona as top-level folders", () => {
    renderPage();
    const tree = screen.getByRole("tree", { name: "Folders" });
    const tops = within(tree)
      .getAllByRole("treeitem")
      .filter((n) => n.getAttribute("aria-level") === "1")
      .map((n) => n.textContent);
    expect(tops.map((t) => t?.replace(/\d+$/, ""))).toEqual(["My Resources", "Global", "analyst"]);
  });

  it("highlights the current folder with its ancestors open", () => {
    renderPage("/resources/lib/user/data/weekly");
    expect(node("user", "data/weekly").getAttribute("aria-current")).toBe("location");
    expect(node("user", "data").getAttribute("aria-expanded")).toBe("true");
    expect(node("user", "").getAttribute("aria-expanded")).toBe("true");
  });

  it("opens a folder when a node is clicked and puts it in the address", () => {
    renderPage();
    fireEvent.click(node("user", "drafts"));
    expect(last(navigations)).toBe("/resources/lib/user/drafts");
    expect(node("user", "drafts").getAttribute("aria-current")).toBe("location");
  });

  it("follows the WAI-ARIA tree keys: Right opens, Left closes, Enter opens the folder", () => {
    renderPage("/resources/lib/global/brand");
    const g = node("global", "");
    act(() => g.focus());
    fireEvent.keyDown(g, { key: "ArrowLeft" });
    expect(node("global", "").getAttribute("aria-expanded")).toBe("false");
    fireEvent.keyDown(node("global", ""), { key: "ArrowRight" });
    expect(node("global", "").getAttribute("aria-expanded")).toBe("true");
    fireEvent.keyDown(node("global", ""), { key: "ArrowUp" });
    expect(document.activeElement?.textContent).toContain("My Resources");
    fireEvent.keyDown(document.activeElement!, { key: "Enter" });
    expect(last(navigations)).toBe("/resources");
  });

  it("opens a link to the retired All view on the caller's own folder", () => {
    renderPage("/resources/lib/all");
    expect(node("user", "").getAttribute("aria-current")).toBe("location");
  });

  it("gives an administrator a People folder, one folder per person, read when opened", () => {
    signIn({ is_admin: true });
    vi.mocked(usePeople).mockImplementation(((enabled: boolean) => ({
      data: enabled
        ? { people: [{ scope_id: "sub-other", email: "other@example.com", count: 3 }, { scope_id: "sub-me", email: "me@example.com", count: 4 }] }
        : undefined,
    })) as unknown as typeof usePeople);
    renderPage("/admin/resources", true);
    fireEvent.click(screen.getByTestId("tree-people"));
    // The caller's own files are My Resources, not a person under People.
    expect(screen.queryByTestId("tree-node-person:sub-me:")).toBeNull();
    fireEvent.click(node("person:sub-other", ""));
    expect(last(navigations)).toBe("/admin/resources/lib/person%3Asub-other");
    expect(screen.getByTestId("path-bar").textContent).toContain("People");
  });

  it("gives nobody else a People folder", () => {
    renderPage();
    expect(screen.queryByTestId("tree-people")).toBeNull();
  });
});

describe("one listing of the folder in view", () => {
  it("lists folders first, then files, in one table", () => {
    renderPage();
    const keys = within(screen.getByTestId("listing"))
      .getAllByRole("row")
      .map((r) => r.getAttribute("data-key"))
      .filter(Boolean);
    expect(keys).toEqual(["d:data/weekly", "f-orders", "f-regions"]);
  });

  it("asks for this level's own files only", () => {
    renderPage();
    expect(last(listings)).toMatchObject({ scope: "user", path: "data", direct: true, sort: "name" });
  });

  it("sorts on the server when a header is clicked, and toggles the direction", () => {
    renderPage();
    fireEvent.click(screen.getByTestId("sort-size"));
    expect(last(navigations)).toBe("/resources/lib/user/data?sort=size_desc");
    expect(last(listings)).toMatchObject({ sort: "size_desc" });
    fireEvent.click(screen.getByTestId("sort-size"));
    expect(last(navigations)).toBe("/resources/lib/user/data?sort=size");
    fireEvent.click(screen.getByTestId("sort-name"));
    expect(last(navigations)).toBe("/resources/lib/user/data");
  });

  it("shows the same entries as tiles", () => {
    renderPage();
    fireEvent.click(screen.getByRole("button", { name: "Tile view" }));
    expect(screen.getByTestId("tile-d:data/weekly")).toBeTruthy();
    expect(screen.getByTestId("tile-f-orders")).toBeTruthy();
    expect(screen.getByTestId("tile-f-regions")).toBeTruthy();
  });

  it("draws a folder and a file as the same row, not a card", () => {
    renderPage();
    expect(row("d:data/weekly").tagName).toBe("TR");
    expect(row("f-orders").tagName).toBe("TR");
  });

  it("counts what this level holds on the status line", () => {
    renderPage();
    expect(screen.getByTestId("status-line").textContent).toContain("1 folder, 2 files");
  });

  it("lists a stored empty folder, and says so inside it", () => {
    seed({ user: held.user! }, { user: ["archive"] });
    renderPage("/resources/lib/user");
    expect(row("d:archive")).toBeTruthy();
    fireEvent.click(row("d:archive"));
    expect(screen.getByTestId("resources-empty").textContent).toContain("This folder is empty");
    expect(screen.getByTestId("resources-empty").textContent).toContain("/My Resources/archive");
  });
});

describe("selection and the preview pane", () => {
  it("previews a clicked file without leaving the page", () => {
    renderPage();
    fireEvent.click(row("f-orders"));
    expect(navigations).toEqual([]);
    const pane = screen.getByTestId("preview-file");
    expect(pane.textContent).toContain("orders.csv");
    expect(pane.textContent).toContain("/My Resources/data");
    expect(pane.textContent).toContain("mcp://user/sub-me/data/orders.csv");
    expect(pane.textContent).toContain("me@example.com");
  });

  it("opens a folder on click", () => {
    renderPage();
    fireEvent.click(row("d:data/weekly"));
    expect(last(navigations)).toBe("/resources/lib/user/data/weekly");
  });

  it("opens a file on double-click, leaving the view in the entry it leaves", () => {
    renderPage();
    fireEvent.doubleClick(row("f-orders"));
    expect(navigations.slice(-2)).toEqual(["/resources/lib/user/data", "/resources/f-orders"]);
  });

  it("selects a range on Shift-click and toggles on Cmd/Ctrl-click", () => {
    renderPage();
    fireEvent.click(row("d:data/weekly"), { metaKey: true });
    fireEvent.click(row("f-regions"), { shiftKey: true });
    expect(screen.getByTestId("selection-bar").textContent).toContain("3 selected");
    fireEvent.click(row("f-orders"), { ctrlKey: true });
    expect(screen.getByTestId("selection-bar").textContent).toContain("2 selected");
  });

  it("shows the count, the total size and the bulk actions for several files", () => {
    renderPage();
    fireEvent.click(row("f-orders"));
    fireEvent.click(row("f-regions"), { shiftKey: true });
    const pane = screen.getByTestId("preview-many");
    expect(pane.textContent).toContain("2 items selected");
    expect(pane.textContent).toContain("3 KB");
    expect(within(pane).getByRole("button", { name: "Move to..." })).toBeTruthy();
    expect(screen.getByTestId("status-line").textContent).toContain("2 selected");
  });

  it("describes the folder in view when nothing is selected", () => {
    renderPage();
    expect(screen.getByTestId("preview-none").textContent).toContain("3 files in this folder");
  });
});

describe("acting on a selection", () => {
  it("moves each selected file into the folder picked, one request each", async () => {
    renderPage();
    fireEvent.click(row("f-orders"));
    fireEvent.click(row("f-regions"), { shiftKey: true });
    fireEvent.click(within(screen.getByTestId("selection-bar")).getByRole("button", { name: "Move to..." }));
    fireEvent.click(within(screen.getByTestId("move-picker")).getByRole("option", { name: "drafts" }));
    expect(screen.getByTestId("move-destination").textContent).toBe("Destination: /My Resources/drafts");
    fireEvent.click(screen.getByRole("button", { name: "Move here" }));
    await waitFor(() => expect(fetchJSON).toHaveBeenCalledTimes(2));
    expect(fetchJSON).toHaveBeenCalledWith("/f-orders", { method: "PATCH", body: JSON.stringify({ path: "drafts" }) });
    expect(fetchJSON).toHaveBeenCalledWith("/f-regions", { method: "PATCH", body: JSON.stringify({ path: "drafts" }) });
  });

  it("reports a refused file beside the ones that moved", async () => {
    fetchJSON.mockImplementation(async (path: string) => {
      if (path === "/f-regions") throw new Error("regions.csv already answers there");
      return {};
    });
    renderPage();
    fireEvent.click(row("f-orders"));
    fireEvent.click(row("f-regions"), { shiftKey: true });
    fireEvent.click(within(screen.getByTestId("selection-bar")).getByRole("button", { name: "Move to..." }));
    fireEvent.click(within(screen.getByTestId("move-picker")).getByRole("option", { name: "drafts" }));
    fireEvent.click(screen.getByRole("button", { name: "Move here" }));
    const report = await screen.findByTestId("action-report");
    expect(report.textContent).toContain("1 of 2 done, 1 refused");
    expect(report.textContent).toContain("regions.csv already answers there");
  });

  it("moves a selected folder in one request that carries its subtree", async () => {
    renderPage();
    fireEvent.click(row("d:data/weekly"), { metaKey: true });
    fireEvent.click(within(screen.getByTestId("selection-bar")).getByRole("button", { name: "Move to..." }));
    // A folder cannot go inside itself, so its own subtree is not offered.
    expect(within(screen.getByTestId("move-picker")).queryByRole("option", { name: "weekly" })).toBeNull();
    fireEvent.click(within(screen.getByTestId("move-picker")).getByRole("option", { name: "drafts" }));
    fireEvent.click(screen.getByRole("button", { name: "Move here" }));
    await waitFor(() =>
      expect(fetchJSON).toHaveBeenCalledWith("/folders/move", {
        method: "POST",
        body: JSON.stringify({ scope: "user", scope_id: "sub-me", from: "data/weekly", to: "drafts/weekly" }),
      }),
    );
  });

  it("adds tags to every selected file, keeping the ones it carries", async () => {
    renderPage();
    fireEvent.click(row("f-regions"));
    fireEvent.click(within(screen.getByTestId("selection-bar")).getByRole("button", { name: "Tag..." }));
    fireEvent.change(screen.getByLabelText(/Tags to add/), { target: { value: "q3" } });
    fireEvent.click(screen.getByRole("button", { name: "Add tags" }));
    await waitFor(() =>
      expect(fetchJSON).toHaveBeenCalledWith("/f-regions", {
        method: "PATCH",
        body: JSON.stringify({ tags: ["reference", "q3"] }),
      }),
    );
  });

  it("deletes a folder's files one at a time, then the folder", async () => {
    fetchJSON.mockImplementation(async (path: string) => {
      if (path.startsWith("?")) return { resources: [file("f-w38", "data/weekly", "w38.csv")], total: 1 };
      return {};
    });
    renderPage();
    fireEvent.click(row("d:data/weekly"), { metaKey: true });
    fireEvent.click(within(screen.getByTestId("selection-bar")).getByRole("button", { name: "Delete" }));
    expect((await screen.findByTestId("delete-names")).textContent).toContain("weekly/, w38.csv");
    fireEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Delete" }));
    await waitFor(() => expect(fetchRaw).toHaveBeenCalledTimes(2));
    expect(fetchRaw.mock.calls[0]![0]).toBe("/f-w38");
    expect(fetchRaw.mock.calls[1]![0]).toBe("/folders");
    expect(JSON.parse(fetchRaw.mock.calls[1]![1].body as string)).toEqual({
      scope: "user",
      scope_id: "sub-me",
      path: "data/weekly",
    });
  });
});

describe("dragging rows", () => {
  function drag(fromKey: string, onto: HTMLElement) {
    const types: string[] = [];
    const data: Record<string, string> = {};
    const dataTransfer = {
      types,
      setData: (t: string, v: string) => {
        types.push(t);
        data[t] = v;
      },
      getData: (t: string) => data[t] ?? "",
      effectAllowed: "",
    };
    fireEvent.dragStart(row(fromKey), { dataTransfer });
    fireEvent.dragOver(onto, { dataTransfer });
    fireEvent.drop(onto, { dataTransfer });
  }

  it("moves a row dropped on a folder row, and Undo puts it back", async () => {
    renderPage();
    drag("f-orders", row("d:data/weekly"));
    await waitFor(() =>
      expect(fetchJSON).toHaveBeenCalledWith("/f-orders", { method: "PATCH", body: JSON.stringify({ path: "data/weekly" }) }),
    );
    const toast = await screen.findByTestId("toast");
    expect(toast.textContent).toContain("Moved 1 item to /My Resources/data/weekly");
    fireEvent.click(within(toast).getByRole("button", { name: "Undo" }));
    await waitFor(() =>
      expect(fetchJSON).toHaveBeenLastCalledWith("/f-orders", { method: "PATCH", body: JSON.stringify({ path: "data" }) }),
    );
  });

  it("moves a row dropped on a tree node or a path segment", async () => {
    renderPage("/resources/lib/user/data/weekly");
    drag("f-w38", screen.getByTestId("crumb-data"));
    await waitFor(() =>
      expect(fetchJSON).toHaveBeenCalledWith("/f-w38", { method: "PATCH", body: JSON.stringify({ path: "data" }) }),
    );
    drag("f-w38", node("user", "drafts"));
    await waitFor(() =>
      expect(fetchJSON).toHaveBeenCalledWith("/f-w38", { method: "PATCH", body: JSON.stringify({ path: "drafts" }) }),
    );
  });
});

describe("the context menu, inline rename and the keyboard", () => {
  it("offers Open, Rename, Move, Tag, Copy URI and Delete on a row", () => {
    renderPage();
    fireEvent.contextMenu(row("f-orders"));
    const items = within(screen.getByTestId("context-menu"))
      .getAllByRole("menuitem")
      .map((b) => b.textContent?.replace(/(Enter|F2|⌘⌫)$/, ""));
    expect(items).toEqual(["Open", "Rename", "Move to...", "Tag...", "Copy URI", "Delete"]);
  });

  it("offers New folder and Upload here on empty space", () => {
    renderPage();
    fireEvent.contextMenu(screen.getByTestId("listing").parentElement!);
    const items = within(screen.getByTestId("context-menu")).getAllByRole("menuitem").map((b) => b.textContent);
    expect(items).toEqual(["New folder", "Upload here"]);
  });

  it("renames a file inline on F2", async () => {
    renderPage();
    fireEvent.click(row("f-orders"));
    fireEvent.keyDown(row("f-orders"), { key: "F2" });
    const field = screen.getByTestId("name-field");
    fireEvent.change(field, { target: { value: "orders-2026.csv" } });
    fireEvent.keyDown(field, { key: "Enter" });
    await waitFor(() =>
      expect(fetchJSON).toHaveBeenCalledWith("/f-orders", {
        method: "PATCH",
        body: JSON.stringify({ display_name: "orders-2026.csv" }),
      }),
    );
  });

  it("renames a folder by moving its subtree", async () => {
    renderPage();
    fireEvent.contextMenu(row("d:data/weekly"));
    fireEvent.click(screen.getByRole("menuitem", { name: /Rename/ }));
    const field = screen.getByTestId("name-field");
    fireEvent.change(field, { target: { value: "monthly" } });
    fireEvent.keyDown(field, { key: "Enter" });
    await waitFor(() =>
      expect(fetchJSON).toHaveBeenCalledWith("/folders/move", {
        method: "POST",
        body: JSON.stringify({ scope: "user", scope_id: "sub-me", from: "data/weekly", to: "data/monthly" }),
      }),
    );
  });

  it("creates a folder inline, stored even with nothing in it", async () => {
    renderPage();
    fireEvent.click(screen.getByRole("button", { name: "New folder" }));
    const field = screen.getByTestId("name-field");
    expect((field as HTMLInputElement).value).toBe("New folder");
    fireEvent.change(field, { target: { value: "archive" } });
    fireEvent.keyDown(field, { key: "Enter" });
    await waitFor(() => expect(fetchRaw).toHaveBeenCalled());
    expect(fetchRaw.mock.calls[0]![0]).toBe("/folders");
    expect(fetchRaw.mock.calls[0]![1].method).toBe("POST");
    expect(JSON.parse(fetchRaw.mock.calls[0]![1].body as string)).toEqual({
      scope: "user",
      scope_id: "sub-me",
      path: "data/archive",
    });
  });

  it("moves with Up/Down, extends with Shift, opens with Enter and goes up with Backspace", () => {
    renderPage();
    const listing = screen.getByTestId("listing").parentElement!;
    fireEvent.keyDown(listing, { key: "ArrowDown" });
    expect(row("d:data/weekly").getAttribute("aria-selected")).toBe("true");
    fireEvent.keyDown(listing, { key: "ArrowDown" });
    fireEvent.keyDown(listing, { key: "ArrowDown", shiftKey: true });
    expect(row("f-orders").getAttribute("aria-selected")).toBe("true");
    expect(row("f-regions").getAttribute("aria-selected")).toBe("true");
    fireEvent.keyDown(listing, { key: "Escape" });
    expect(screen.queryByTestId("selection-bar")).toBeNull();
    fireEvent.keyDown(listing, { key: "a", metaKey: true });
    expect(screen.getByTestId("selection-bar").textContent).toContain("3 selected");
    fireEvent.keyDown(listing, { key: "Backspace" });
    // My Resources at its root is the page's own plain address.
    expect(last(navigations)).toBe("/resources");
  });

  it("steps Back and Forward through the places visited", () => {
    renderPage();
    fireEvent.click(row("d:data/weekly"));
    fireEvent.click(screen.getByRole("button", { name: "Back" }));
    expect(last(navigations)).toBe("/resources/lib/user/data");
    fireEvent.click(screen.getByRole("button", { name: "Forward" }));
    expect(last(navigations)).toBe("/resources/lib/user/data/weekly");
    fireEvent.click(screen.getByRole("button", { name: "Enclosing folder" }));
    expect(last(navigations)).toBe("/resources/lib/user/data");
  });
});

describe("search", () => {
  it("searches the whole top-level folder from inside a folder, each hit with its location", async () => {
    vi.useFakeTimers();
    try {
      renderPage("/resources/lib/user/data/weekly");
      fireEvent.change(screen.getByLabelText("Search"), { target: { value: "plan" } });
      await act(async () => {
        vi.advanceTimersByTime(400);
      });
    } finally {
      vi.useRealTimers();
    }
    expect(last(listings)).toMatchObject({ scope: "user", q: "plan", path: undefined, direct: false });
    expect(screen.getByTestId("search-note").textContent).toContain("Searching everything in My Resources");
    expect(row("f-plan").textContent).toContain("/My Resources/drafts");
  });

  it("reveals a hit in its folder with the hit selected", () => {
    renderPage("/resources/lib/user/data?q=plan");
    fireEvent.click(within(row("f-plan")).getByRole("button", { name: "Show in folder" }));
    expect(last(navigations)).toBe("/resources/lib/user/drafts");
    expect(row("f-plan").getAttribute("aria-selected")).toBe("true");
    expect(node("user", "drafts").getAttribute("aria-current")).toBe("location");
  });
});

describe("what the page offers depends on who is reading", () => {
  it("offers New folder and Upload in the caller's own folder", () => {
    renderPage();
    expect(screen.getByRole("button", { name: "New folder" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Upload" })).toBeTruthy();
  });

  it("withholds them on Global, naming who publishes there", () => {
    seed({ ...held, global: [] });
    renderPage("/resources/lib/global");
    expect(screen.queryByRole("button", { name: "Upload" })).toBeNull();
    expect(screen.queryByRole("button", { name: "New folder" })).toBeNull();
    expect(screen.getByTestId("resources-read-only").textContent).toContain("Published by platform administrators");
  });

  it("offers them to a platform administrator on Global", () => {
    signIn({ is_admin: true });
    renderPage("/resources/lib/global/brand");
    expect(screen.getByRole("button", { name: "Upload" })).toBeTruthy();
  });

  it("files an upload into the folder in view", () => {
    renderPage();
    fireEvent.keyDown(screen.getByRole("button", { name: "Upload" }), { key: "Enter" });
    fireEvent.click(screen.getByRole("menuitem", { name: "Many files or a folder..." }));
    expect(screen.getByTestId("upload-destination").textContent).toContain("My Resources");
    expect(within(screen.getByRole("dialog")).getByRole("combobox", { name: "Base folder" }).textContent).toContain("data");
  });
});

describe("the page's language", () => {
  it("never says library", () => {
    signIn({ is_admin: true });
    const { container } = renderPage("/admin/resources/lib/user/data", true);
    fireEvent.click(row("f-orders"));
    fireEvent.contextMenu(row("f-orders"));
    expect(container.ownerDocument.body.textContent?.toLowerCase()).not.toContain("library");
  });
});
