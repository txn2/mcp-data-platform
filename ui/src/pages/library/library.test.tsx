import { describe, it, expect, vi, beforeEach } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

const lib = vi.hoisted(() => ({
  recents: [] as unknown[],
  favorites: [] as unknown[],
  trash: { items: [] as unknown[], total: 0, retention_days: 30 },
  unused: { items: [] as unknown[], total: 0, days: 90 },
  groups: [] as unknown[],
  mutate: vi.fn(),
  mutateAsync: vi.fn().mockResolvedValue(undefined),
}));

vi.mock("@/api/portal/hooks/library", () => {
  const mutation = () => ({ mutate: lib.mutate, mutateAsync: lib.mutateAsync, isPending: false, error: null });
  return {
    useRecents: () => ({ data: lib.recents, isLoading: false }),
    useFavorites: () => ({ data: lib.favorites, isLoading: false }),
    useTrash: () => ({ data: lib.trash, isLoading: false }),
    useUnused: () => ({ data: lib.unused, isLoading: false }),
    useDuplicates: () => ({ data: lib.groups, isLoading: false }),
    useRestore: mutation,
    usePurge: mutation,
    useDismissGroup: mutation,
    useDeprecateAsset: mutation,
    useTrashAsset: mutation,
  };
});
vi.mock("@/api/portal/hooks/threads", () => ({
  usePractitionerWorklist: () => ({ data: { data: [{ id: "t1", asset_id: "a9", target_label: "Churn dashboard", first_message: "Sign-off requested" }] }, isLoading: false }),
  useSMEWorklist: () => ({ data: { data: [] }, isLoading: false }),
}));
vi.mock("@/api/portal/client", () => ({
  apiFetch: vi.fn().mockResolvedValue({ data: [{ id: "d1", name: "Pricing test readout" }], total: 1 }),
}));

import { HomePage, itemPath } from "./HomePage";
import { TrashPage } from "./TrashPage";
import { UnusedPage } from "./UnusedPage";
import { DuplicatesPage } from "./DuplicatesPage";
import { useAuthStore } from "@/stores/auth";

function renderWith(ui: React.ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={qc}>{ui}</QueryClientProvider>);
}

const now = new Date().toISOString();

beforeEach(() => {
  lib.mutate.mockClear();
  lib.mutateAsync.mockClear();
  lib.recents = [];
  lib.favorites = [];
  lib.trash = { items: [], total: 0, retention_days: 30 };
  lib.unused = { items: [], total: 0, days: 90 };
  lib.groups = [];
  useAuthStore.setState({ user: { user_id: "u", roles: [], is_admin: false } });
});

describe("HomePage (#2504)", () => {
  it("lists recent work, favorites, drafts and what is waiting, each opening its item", async () => {
    lib.recents = [{ kind: "asset", id: "a1", name: "Q3 revenue by region", at: now }];
    lib.favorites = [{ kind: "collection", id: "c1", name: "Finance reports", at: now }];
    const onNavigate = vi.fn();
    renderWith(<HomePage onNavigate={onNavigate} />);

    expect(screen.getByRole("heading", { name: "Home" })).toBeTruthy();
    fireEvent.click(screen.getByText("Q3 revenue by region"));
    expect(onNavigate).toHaveBeenCalledWith("/assets/a1");
    fireEvent.click(screen.getByText("Finance reports"));
    expect(onNavigate).toHaveBeenCalledWith("/collections/c1");
    fireEvent.click(screen.getByText("Churn dashboard"));
    expect(onNavigate).toHaveBeenCalledWith("/assets/a9");
    expect(await screen.findByText("Pricing test readout")).toBeTruthy();
  });

  it("says what each empty section is for", () => {
    renderWith(<HomePage onNavigate={vi.fn()} />);
    expect(screen.getByText(/Nothing opened yet/)).toBeTruthy();
    expect(screen.getByText(/Star an asset or collection/)).toBeTruthy();
  });

  it("opens an entry by its kind", () => {
    expect(itemPath({ kind: "asset", id: "x" })).toBe("/assets/x");
    expect(itemPath({ kind: "collection", id: "y" })).toBe("/collections/y");
  });
});

describe("TrashPage (#2509)", () => {
  it("restores and confirms a permanent delete", () => {
    const soon = new Date(Date.now() + 2 * 86400000).toISOString();
    lib.trash = {
      items: [{ kind: "asset", id: "a1", name: "Old pricing model", deleted_at: now, purge_at: soon }],
      total: 1,
      retention_days: 30,
    };
    renderWith(<TrashPage onNavigate={vi.fn()} />);
    expect(screen.getAllByText("Old pricing model").length).toBeGreaterThan(0);
    fireEvent.click(screen.getAllByRole("button", { name: "Restore" })[0]!);
    expect(lib.mutate).toHaveBeenCalledWith({ kind: "asset", id: "a1" }, expect.anything());

    fireEvent.click(screen.getAllByRole("button", { name: "Delete permanently" })[0]!);
    expect(screen.getByText(/cannot be restored/)).toBeTruthy();
  });

  it("offers everyone's trash to an administrator only", () => {
    renderWith(<TrashPage onNavigate={vi.fn()} />);
    expect(screen.queryByText("Everyone's trash")).toBeNull();
    expect(screen.getByText(/Trash is empty/)).toBeTruthy();
  });
});

describe("UnusedPage (#2509)", () => {
  it("deprecates the selected assets", async () => {
    lib.unused = {
      items: [{ id: "a1", name: "Vendor list export", status: "draft", opens: 0, created_at: now }],
      total: 1,
      days: 90,
    };
    renderWith(<UnusedPage onNavigate={vi.fn()} />);
    expect(screen.getByText("Never")).toBeTruthy();
    fireEvent.click(screen.getByLabelText("Select Vendor list export"));
    fireEvent.click(screen.getByRole("button", { name: "Deprecate" }));
    await vi.waitFor(() => expect(lib.mutateAsync).toHaveBeenCalledWith({ id: "a1", note: expect.stringContaining("90") }));
  });

  it("says when every asset was opened", () => {
    renderWith(<UnusedPage onNavigate={vi.fn()} />);
    expect(screen.getByText(/every asset you own was opened/)).toBeTruthy();
  });
});

describe("DuplicatesPage (#2509)", () => {
  it("deprecates a member as a duplicate of the kept one and dismisses the group", () => {
    lib.groups = [
      {
        digest: "d".repeat(64),
        similarity: 96,
        members: [
          { id: "a1", name: "Q3 revenue", version: 4, updated_at: now, opens: 41, status: "published" },
          { id: "a2", name: "Q3 revenue (copy)", version: 1, updated_at: now, opens: 2 },
        ],
      },
    ];
    renderWith(<DuplicatesPage onNavigate={vi.fn()} />);
    expect(screen.getByRole("heading", { name: "2 assets, 96% similar" })).toBeTruthy();
    fireEvent.click(screen.getAllByRole("button", { name: "Deprecate" })[1]!);
    expect(lib.mutate).toHaveBeenCalledWith({ id: "a2", note: "Duplicate of Q3 revenue" });
    fireEvent.click(screen.getByRole("button", { name: "Dismiss group" }));
    expect(lib.mutate).toHaveBeenCalledWith({ digest: "d".repeat(64) }, expect.anything());
  });

  it("says when nothing looks alike", () => {
    renderWith(<DuplicatesPage onNavigate={vi.fn()} />);
    expect(screen.getByText(/no two of your assets look alike/)).toBeTruthy();
  });
});
