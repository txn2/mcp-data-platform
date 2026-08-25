import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, cleanup, fireEvent, waitFor, within } from "@testing-library/react";
import { useAuthStore, type UserProfile } from "@/stores/auth";

const uploadResource = vi.hoisted(() => vi.fn());

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

describe("the Resources page offers Upload only where the caller may add", () => {
  it("offers it on the caller's own library", () => {
    render(<ResourcesPage />);
    expect(screen.getByRole("button", { name: "Upload" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Upload Resource" })).toBeTruthy();
    expect(screen.queryByTestId("scope-read-only")).toBeNull();
  });

  it("withholds it on the global library, naming who publishes there instead", () => {
    render(<ResourcesPage />);
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
    render(<ResourcesPage />);
    selectTab("analyst");

    expect(screen.queryByRole("button", { name: "Upload" })).toBeNull();
    expect(screen.getByTestId("scope-read-only").textContent).toContain(
      "Published by the analyst persona's administrators",
    );
  });

  it("offers it on a persona library the caller administers", () => {
    signIn({ roles: ["dp_analyst", "dp_persona-admin:analyst"] });
    render(<ResourcesPage />);
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
    render(<ResourcesPage />);
    selectTab("Global");

    expect(screen.queryByRole("button", { name: "Upload" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Upload Resource" })).toBeNull();
  });

  it("withholds it from a platform admin on the user page's persona library", () => {
    signIn({ is_admin: true });
    render(<ResourcesPage />);
    selectTab("analyst");

    expect(screen.queryByRole("button", { name: "Upload" })).toBeNull();
  });

  it("still offers it to a platform admin on their own library", () => {
    signIn({ is_admin: true });
    render(<ResourcesPage />);

    expect(screen.getByRole("button", { name: "Upload" })).toBeTruthy();
  });

  // Withholding a control from somebody who holds the authority has to name
  // where the authority is exercised, or it reads as the platform having lost
  // track of who they are.
  it("tells the platform admin where they do add to the library instead", () => {
    signIn({ is_admin: true });
    render(<ResourcesPage />);
    selectTab("Global");

    expect(screen.getByTestId("scope-read-only").textContent).toContain(
      "Add to it from Admin > Resources.",
    );
  });

  it("says no such thing to a reader who has no such authority", () => {
    render(<ResourcesPage />);
    selectTab("Global");

    expect(screen.getByTestId("scope-read-only").textContent).not.toContain("Admin > Resources");
  });

  it("keeps every library writable in the administrator's own section", () => {
    signIn({ is_admin: true });
    render(<ResourcesPage admin />);
    selectTab("Global");

    expect(screen.getByRole("button", { name: "Upload" })).toBeTruthy();
    expect(screen.queryByTestId("scope-read-only")).toBeNull();
  });
});

describe("the upload dialog states where the file will land", () => {
  it("names the caller's own library before a file is chosen", () => {
    render(<ResourcesPage />);
    fireEvent.click(screen.getByRole("button", { name: "Upload" }));

    const destination = screen.getByTestId("upload-destination");
    expect(destination.textContent).toContain("My Resources");
    expect(destination.textContent).toContain("Only you can see it.");
    // A destination stated only after a file is picked would be stated too late.
    expect(screen.getByText("Choose file (max 100 MB)")).toBeTruthy();
  });

  it("names the persona library when that is the tab in view", () => {
    signIn({ roles: ["dp_analyst", "dp_persona-admin:analyst"] });
    render(<ResourcesPage />);
    selectTab("analyst");
    fireEvent.click(screen.getByRole("button", { name: "Upload" }));

    expect(screen.getByTestId("upload-destination").textContent).toContain("analyst persona");
  });

  it("leaves the admin page its scope picker rather than a fixed line", () => {
    signIn({ is_admin: true, roles: ["admin"] });
    render(<ResourcesPage admin />);
    fireEvent.click(screen.getByRole("button", { name: "Upload" }));

    expect(screen.getByLabelText("Scope")).toBeTruthy();
    expect(screen.queryByTestId("upload-destination")).toBeNull();
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
    const { container } = render(<ResourcesPage />);
    fireEvent.click(screen.getByRole("button", { name: "Upload" }));

    const form = await fillAndSubmit(container);
    expect(form.get("scope")).toBe("user");
    expect(form.get("scope_id")).toBe("analyst@example.com");
  });

  it("sends the persona scope from a persona tab the caller administers", async () => {
    signIn({ roles: ["dp_analyst", "dp_persona-admin:analyst"] });
    const { container } = render(<ResourcesPage />);
    selectTab("analyst");
    fireEvent.click(screen.getByRole("button", { name: "Upload" }));

    const form = await fillAndSubmit(container);
    expect(form.get("scope")).toBe("persona");
    expect(form.get("scope_id")).toBe("analyst");
  });
});


// A resource used to open in a dialog over the library. It opens at an address
// of its own now, which is a navigation -- and a navigation unmounts the
// library, so what the library was showing has to live in the address bar or it
// is gone by the time the reader presses Back (#1470).

const LISTED: Resource = {
  id: "res-1",
  scope: "user",
  scope_id: "analyst@example.com",
  category: "references",
  filename: "seasonal-factors.csv",
  display_name: "Seasonal Factors",
  description: "Monthly demand multipliers.",
  mime_type: "text/csv",
  size_bytes: 64,
  s3_key: "k",
  uri: "mcp://resources/analyst/seasonal-factors.csv",
  tags: [],
  uploader_sub: "analyst@example.com",
  uploader_email: "analyst@example.com",
  created_at: "2026-08-03T10:00:00Z",
  updated_at: "2026-08-17T10:00:00Z",
};

function listing(resources: Resource[]) {
  vi.mocked(useInfiniteResources).mockReturnValue({
    data: { data: resources, total: resources.length },
    isLoading: false,
    hasNextPage: false,
    isFetchingNextPage: false,
    fetchNextPage: vi.fn(),
  } as unknown as ReturnType<typeof useInfiniteResources>);
}

function atAddress(url: string) {
  window.history.replaceState({}, "", url);
}

describe("opening a resource from the library", () => {
  afterEach(() => atAddress("/"));

  it("navigates to the resource's own address", () => {
    listing([LISTED]);
    const onNavigate = vi.fn();
    render(<ResourcesPage onNavigate={onNavigate} />);
    onNavigate.mockClear();

    fireEvent.click(screen.getByText("Seasonal Factors"));
    expect(onNavigate).toHaveBeenCalledWith("/resources/res-1");
  });

  // A filter typed and clicked through inside the 300ms debounce window has not
  // reached the address bar yet. What is pinned is what is on screen.
  it("pins a search still inside the debounce window", () => {
    listing([LISTED]);
    const onNavigate = vi.fn();
    render(<ResourcesPage onNavigate={onNavigate} />);
    fireEvent.change(screen.getByLabelText("Search resources"), { target: { value: "demand" } });
    onNavigate.mockClear();

    fireEvent.click(screen.getByText("Seasonal Factors"));
    expect(onNavigate.mock.calls[0]).toEqual(["/resources?q=demand", { replace: true }]);
  });

  it("pins the view into the entry it leaves, so Back returns to this library", () => {
    listing([LISTED]);
    const onNavigate = vi.fn();
    render(<ResourcesPage onNavigate={onNavigate} />);
    selectTab("Global");
    onNavigate.mockClear();

    fireEvent.click(screen.getByText("Seasonal Factors"));
    // The view is written over the entry being left before the resource is
    // pushed onto a new one.
    expect(onNavigate.mock.calls).toEqual([
      ["/resources?tab=global", { replace: true }],
      ["/resources/res-1"],
    ]);
  });

  it("keeps the administrator inside their own section", () => {
    listing([LISTED]);
    const onNavigate = vi.fn();
    render(<ResourcesPage admin onNavigate={onNavigate} />);
    onNavigate.mockClear();

    fireEvent.click(screen.getByText("Seasonal Factors"));
    expect(onNavigate).toHaveBeenCalledWith("/admin/resources/res-1");
  });
});

describe("the library's view lives in its address", () => {
  afterEach(() => {
    listing([]);
    atAddress("/");
  });

  it("writes the scope tab into the address without pushing a history entry", () => {
    listing([]);
    const onNavigate = vi.fn();
    render(<ResourcesPage onNavigate={onNavigate} />);
    onNavigate.mockClear();

    selectTab("Global");
    expect(onNavigate).toHaveBeenCalledWith("/resources?tab=global", { replace: true });
  });

  it("leaves the plain library its plain address", () => {
    listing([]);
    const onNavigate = vi.fn();
    render(<ResourcesPage onNavigate={onNavigate} />);
    expect(onNavigate).toHaveBeenCalledWith("/resources", { replace: true });
  });

  it("opens on the scope and the filters its address names", () => {
    listing([]);
    atAddress("/resources?tab=global&q=demand&category=references&tag=q3");
    render(<ResourcesPage onNavigate={vi.fn()} />);

    expect(screen.getByRole("tab", { name: "Global" }).getAttribute("aria-selected")).toBe("true");
    expect((screen.getByLabelText("Search resources") as HTMLInputElement).value).toBe("demand");
    expect(useInfiniteResources).toHaveBeenCalledWith(
      expect.objectContaining({
        scope: "global",
        q: "demand",
        category: "references",
        tag: "q3",
      }),
    );
  });
});

// Tags are stored, indexed, and filterable on the server, and were filterable
// everywhere except on the page that shows them: the library hook never set the
// `tag` parameter and no control would have (#1471).

const TAGGED: Resource = {
  ...LISTED,
  id: "res-2",
  category: "data",
  display_name: "Q3 Rates",
  tags: ["q3", "finance"],
};

async function chooseTag(name: string) {
  fireEvent.click(screen.getByLabelText("Filter by tag"));
  fireEvent.click(await screen.findByRole("option", { name }));
}

describe("the library's tag filter", () => {
  afterEach(() => {
    listing([]);
    atAddress("/");
  });

  it("offers the tags the resources in view carry", async () => {
    listing([LISTED, TAGGED]);
    render(<ResourcesPage onNavigate={vi.fn()} />);

    fireEvent.click(screen.getByLabelText("Filter by tag"));
    expect(await screen.findByRole("option", { name: "finance" })).toBeTruthy();
    expect(screen.getByRole("option", { name: "q3" })).toBeTruthy();
  });

  it("narrows the request to the tag chosen, alongside the category filter", async () => {
    listing([LISTED, TAGGED]);
    render(<ResourcesPage onNavigate={vi.fn()} />);
    await chooseTag("q3");

    expect(useInfiniteResources).toHaveBeenLastCalledWith(
      expect.objectContaining({ tag: "q3", scope: "user" }),
    );
  });

  // A tag that matched nothing is a filter that missed, not a library nobody
  // has uploaded to; the two send the reader to different places.
  it("reads a tag that matched nothing as a filter, not as an empty library", async () => {
    listing([TAGGED]);
    const { rerender } = render(<ResourcesPage onNavigate={vi.fn()} />);
    await chooseTag("q3");
    listing([]);
    rerender(<ResourcesPage onNavigate={vi.fn()} />);

    expect(screen.getByTestId("resources-empty").textContent).toContain("No resources match");
  });

  // The narrowed view holds only resources carrying the tag, so the facet built
  // from it would otherwise stop naming the choice that was made.
  it("keeps naming the chosen tag once the view has narrowed to it", async () => {
    listing([TAGGED]);
    render(<ResourcesPage onNavigate={vi.fn()} />);
    await chooseTag("q3");

    expect(screen.getByLabelText("Filter by tag").textContent).toContain("q3");
  });

  it("goes inert on a library nobody has tagged", () => {
    listing([LISTED]);
    render(<ResourcesPage onNavigate={vi.fn()} />);

    expect((screen.getByLabelText("Filter by tag") as HTMLButtonElement).disabled).toBe(true);
    // ... and not on one that has tags to offer, or the assertion above would
    // hold whatever the control did.
    expect((screen.getByLabelText("Filter by category") as HTMLButtonElement).disabled).toBe(false);
  });

  // Both filters survive a scope change, the way the category filter always
  // has: a reader moving between libraries is carrying a question with them.
  it("carries both filters across a scope tab change", async () => {
    listing([LISTED, TAGGED]);
    render(<ResourcesPage onNavigate={vi.fn()} />);
    await chooseTag("q3");
    selectTab("Global");

    expect(useInfiniteResources).toHaveBeenLastCalledWith(
      expect.objectContaining({ tag: "q3", scope: "global" }),
    );
  });
});
