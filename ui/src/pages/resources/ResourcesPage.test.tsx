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

  it("offers it on every tab to a platform admin reading the user page", () => {
    signIn({ is_admin: true });
    render(<ResourcesPage />);
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
