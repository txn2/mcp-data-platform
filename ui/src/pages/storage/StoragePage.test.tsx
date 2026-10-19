import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup, within } from "@testing-library/react";
import { mockStorageHistory, mockStorageReport } from "@/mocks/data/storage";

vi.mock("@/api/admin/hooks", () => ({
  useStorageReport: vi.fn(),
  useStorageHistory: vi.fn(),
}));

import { useStorageHistory, useStorageReport } from "@/api/admin/hooks";
import { StoragePage } from "./StoragePage";

const asked: { store: string; days: number }[] = [];

beforeEach(() => {
  vi.clearAllMocks();
  asked.length = 0;
  vi.mocked(useStorageReport).mockReturnValue({ data: mockStorageReport(), isLoading: false, isError: false } as unknown as ReturnType<
    typeof useStorageReport
  >);
  vi.mocked(useStorageHistory).mockImplementation((store: string, days: number) => {
    asked.push({ store, days });
    return { data: mockStorageHistory(store, days), isLoading: false } as unknown as ReturnType<typeof useStorageHistory>;
  });
});

afterEach(cleanup);

function row(name: string): HTMLElement {
  return screen.getByText(name, { selector: "td" }).closest("tr")!;
}

describe("StoragePage (#2148)", () => {
  it("shows every store used and free, its growth, when it fills and its level", () => {
    render(<StoragePage onNavigate={vi.fn()} />);
    const bucket = row("Bucket acme-portal-assets");
    expect(within(bucket).getByText("462 GB")).toBeInTheDocument();
    expect(within(bucket).getByText("92.4%")).toBeInTheDocument();
    expect(within(bucket).getByText("Critical")).toBeInTheDocument();
    expect(within(bucket).getByText(/at the 30d rate/)).toBeInTheDocument();
    expect(within(row("Bucket acme-resources")).getByText("No capacity known")).toBeInTheDocument();
    expect(within(row("Database")).getAllByText(/\/day/)).toHaveLength(2);
  });

  // #2201: a new store's first hours are no daily rate, and the monthly rate
  // waits for a week instead of repeating the weekly one.
  it("says how much history a rate still needs instead of projecting a burst", () => {
    render(<StoragePage onNavigate={vi.fn()} />);
    const spool = row("Webhook spool on mcp-data-platform-0");
    expect(within(spool).getAllByText("Not enough history")).toHaveLength(3);
    expect(within(spool).getByText("1.7 h of 1 d")).toBeInTheDocument();
    expect(within(spool).getByText("1.7 h of 7 d")).toBeInTheDocument();
    expect(within(spool).queryByText(/\/day/)).toBeNull();

    const resources = row("Bucket acme-resources");
    expect(within(resources).getByText("+204.8 MB/day")).toBeInTheDocument();
    expect(within(resources).getByText("3 d of 7 d")).toBeInTheDocument();
  });

  it("links a store with no capacity to the setting that gives it one", () => {
    const onNavigate = vi.fn();
    render(<StoragePage onNavigate={onNavigate} />);
    fireEvent.click(within(row("Bucket acme-resources")).getByRole("button", { name: "No capacity known" }));
    expect(onNavigate).toHaveBeenCalledWith("/admin/settings/storage");
    expect(asked).toEqual([]);
  });

  it("opens a store's history on its row and changes the span", () => {
    render(<StoragePage onNavigate={vi.fn()} />);
    fireEvent.click(row("SeaweedFS cluster"));
    expect(screen.getByText("History: SeaweedFS cluster")).toBeInTheDocument();
    expect(asked[asked.length - 1]).toEqual({ store: "seaweedfs", days: 30 });
    fireEvent.click(screen.getByRole("button", { name: "90 days" }));
    expect(asked[asked.length - 1]).toEqual({ store: "seaweedfs", days: 90 });
    fireEvent.click(row("SeaweedFS cluster"));
    expect(screen.queryByText("History: SeaweedFS cluster")).toBeNull();
  });

  it("lists the buckets with what the reconcile found, the spools and the tables", () => {
    render(<StoragePage onNavigate={vi.fn()} />);
    expect(screen.getByText("Buckets")).toBeInTheDocument();
    expect(screen.getByText("12")).toBeInTheDocument();
    expect(screen.getByText("Never")).toBeInTheDocument();
    expect(screen.getByText("Webhook spools")).toBeInTheDocument();
    expect(screen.getByText(/Last full listing/)).toBeInTheDocument();
  });

  it("goes to the storage settings", () => {
    const onNavigate = vi.fn();
    render(<StoragePage onNavigate={onNavigate} />);
    fireEvent.click(screen.getByRole("button", { name: "Storage settings" }));
    expect(onNavigate).toHaveBeenCalledWith("/admin/settings/storage");
  });

  it("says when the cluster could not be read, and when nothing could", () => {
    const report = mockStorageReport();
    report.seaweedfs!.error = "no SeaweedFS volume server answered";
    vi.mocked(useStorageReport).mockReturnValue({ data: report, isLoading: false, isError: false } as unknown as ReturnType<
      typeof useStorageReport
    >);
    render(<StoragePage onNavigate={vi.fn()} />);
    expect(screen.getByText(/could not be read: no SeaweedFS volume server answered/)).toBeInTheDocument();
    cleanup();
    vi.mocked(useStorageReport).mockReturnValue({ data: undefined, isLoading: false, isError: true } as unknown as ReturnType<
      typeof useStorageReport
    >);
    render(<StoragePage onNavigate={vi.fn()} />);
    expect(screen.getByText(/Storage could not be read/)).toBeInTheDocument();
  });

  it("names only the capacity sources the report carries", () => {
    render(<StoragePage onNavigate={vi.fn()} />);
    expect(
      screen.getByText(/Capacity is the database limit, bucket budgets, the disks the SeaweedFS cluster reports and each spool's volume\./),
    ).toBeInTheDocument();
    cleanup();
    const report = mockStorageReport();
    report.seaweedfs = null;
    report.stores = report.stores.filter((s) => s.kind !== "seaweedfs").map((s) => ({ ...s, capacity_from: undefined }));
    report.database.limit_bytes = null;
    vi.mocked(useStorageReport).mockReturnValue({ data: report, isLoading: false, isError: false } as unknown as ReturnType<
      typeof useStorageReport
    >);
    const { container } = render(<StoragePage onNavigate={vi.fn()} />);
    expect(screen.getByText(/Capacity is each spool's volume\./)).toBeInTheDocument();
    expect(container.textContent).not.toMatch(/SeaweedFS|cluster/);
    cleanup();
    report.spools = report.spools.map((s) => ({ ...s, fs_total_bytes: null }));
    render(<StoragePage onNavigate={vi.fn()} />);
    expect(screen.queryByText(/Capacity is/)).toBeNull();
  });

  it("says a store has no samples yet", () => {
    vi.mocked(useStorageHistory).mockReturnValue({ data: { store: "database", days: 30, points: [] }, isLoading: false } as unknown as ReturnType<
      typeof useStorageHistory
    >);
    render(<StoragePage onNavigate={vi.fn()} />);
    fireEvent.click(row("Database"));
    expect(screen.getByText(/No samples yet/)).toBeInTheDocument();
  });
});
