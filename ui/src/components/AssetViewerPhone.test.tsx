import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { AssetViewer } from "./AssetViewer";

// #2601: the asset viewer on a phone. One title block (a back arrow beside a
// title of up to two lines, a quiet line under it), one toolbar row that never
// wraps, and everything else in More.

vi.mock("@/api/portal/hooks", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/portal/hooks")>();
  return { ...actual, useKnowledgeBacklinks: () => ({ data: { pages: [] } }) };
});

const stubMutation = () => ({ mutate: vi.fn(), mutateAsync: vi.fn(), isPending: false, isError: false }) as never;

function asset(overrides: Record<string, unknown> = {}) {
  return {
    id: "a1",
    owner_id: "owner",
    owner_email: "owner@example.com",
    name: "Store Operations Weekly Check-in: Agenda, November 10 (Regional Managers)",
    description: "",
    content_type: "text/markdown",
    s3_bucket: "b",
    s3_key: "k",
    size_bytes: 4,
    tags: [],
    provenance: {},
    current_version: 7,
    created_at: "2026-06-01T00:00:00Z",
    updated_at: "2026-11-10T00:00:00Z",
    ...overrides,
  } as never;
}

const versions = [7, 6, 5, 4].map((v) => ({ version: v, created_at: "2026-11-0" + (v - 3) + "T10:00:00Z", size_bytes: 4, content_type: "text/markdown" }));

function renderViewer(props: Record<string, unknown> = {}) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <AssetViewer
        asset={asset()}
        content={"# hi"}
        isLoading={false}
        contentUrl=""
        onBack={() => {}}
        onNavigate={() => {}}
        updateMutation={stubMutation()}
        deleteMutation={stubMutation()}
        {...props}
      />
    </QueryClientProvider>,
  );
}

// A Radix menu opens on pointerdown with the primary button.
function openMore() {
  fireEvent.pointerDown(screen.getByRole("button", { name: "More" }), { button: 0, ctrlKey: false, pointerType: "mouse" });
  return within(screen.getByRole("menu"));
}

const realWidth = window.innerWidth;
beforeEach(() => Object.defineProperty(window, "innerWidth", { configurable: true, value: 390 }));
afterEach(() => Object.defineProperty(window, "innerWidth", { configurable: true, value: realWidth }));

describe("the asset viewer on a phone", () => {
  it("is a title block and one toolbar row, with the share state under the title", () => {
    renderViewer({ isOwner: false, sharePermission: "editor", phoneToolbarExtra: <button type="button">Feedback, 3 open threads</button> });
    expect(screen.getByTestId("phone-title")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Back" })).toBeInTheDocument();
    expect(screen.getByTestId("phone-subtitle")).toHaveTextContent("Shared with you · Editor");
    expect(screen.getByTestId("phone-subtitle")).toHaveTextContent(/Updated/);
    const row = screen.getByTestId("phone-toolbar");
    expect(within(row).getByRole("button", { name: "Feedback, 3 open threads" })).toBeInTheDocument();
    expect(within(row).getByRole("button", { name: "Expand" })).toBeInTheDocument();
    expect(within(row).getByRole("button", { name: "More" })).toBeInTheDocument();
    // No actions row and no details toggle above the content.
    expect(screen.queryByTestId("page-header-actions")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Show details" })).not.toBeInTheDocument();
    expect(screen.queryByText(/Shared \(Editor\)/)).not.toBeInTheDocument();
  });

  it("puts the version picker in More > Versions, not on the row", () => {
    const onSelectVersion = vi.fn();
    renderViewer({ versions, onSelectVersion, selectedVersion: null });
    expect(screen.queryByRole("combobox", { name: "Asset version" })).not.toBeInTheDocument();
    const menu = openMore();
    fireEvent.click(menu.getByRole("menuitem", { name: /Versions/ }));
    const dialog = within(screen.getByRole("dialog", { name: "Versions" }));
    fireEvent.click(dialog.getByRole("button", { name: /^v4/ }));
    expect(onSelectVersion).toHaveBeenCalledWith(4);
  });

  it("brings the picker and Revert onto the row while an older version is open", () => {
    renderViewer({
      versions,
      onSelectVersion: vi.fn(),
      selectedVersion: 4,
      versionContent: "# old",
      revertMutation: stubMutation(),
      contentUpdateMutation: stubMutation(),
    });
    const row = within(screen.getByTestId("phone-toolbar"));
    expect(row.getByRole("combobox", { name: "Asset version" })).toHaveTextContent("v4");
    expect(row.getByRole("button", { name: /Revert/ })).toBeInTheDocument();
    expect(row.queryByRole("group", { name: "Content view" })).not.toBeInTheDocument();
    expect(screen.getByTestId("phone-subtitle")).toHaveTextContent("Viewing v4 of 7, read-only");
  });

  it("offers an owner Share on the row and the rest in More, Delete last", () => {
    const onDownload = vi.fn();
    renderViewer({ isOwner: true });
    expect(within(screen.getByTestId("phone-toolbar")).getByRole("button", { name: "Share" })).toBeInTheDocument();
    const menu = openMore();
    const items = menu.getAllByRole("menuitem").map((i) => i.textContent);
    expect(items[0]).toBe("Details");
    expect(items).toContain("Download");
    expect(items[items.length - 1]).toBe("Delete");
    fireEvent.click(menu.getByRole("menuitem", { name: "Details" }));
    expect(screen.getByTestId("viewer-details")).toBeInTheDocument();
    expect(onDownload).not.toHaveBeenCalled();
  });

  it("offers a reader who does not own it no Delete and no Share", () => {
    renderViewer({ isOwner: false, sharePermission: "viewer" });
    expect(within(screen.getByTestId("phone-toolbar")).queryByRole("button", { name: "Share" })).not.toBeInTheDocument();
    const menu = openMore();
    expect(menu.queryByRole("menuitem", { name: "Delete" })).not.toBeInTheDocument();
  });

  it("puts Save in place of Expand while there are unsaved edits, and says so under the title", () => {
    renderViewer({ isOwner: true, contentUpdateMutation: stubMutation() });
    const row = within(screen.getByTestId("phone-toolbar"));
    fireEvent.click(row.getByRole("button", { name: "Source" }));
    expect(row.getByRole("button", { name: /Save/ })).toBeInTheDocument();
    expect(row.queryByRole("button", { name: "Expand" })).not.toBeInTheDocument();
  });
});
