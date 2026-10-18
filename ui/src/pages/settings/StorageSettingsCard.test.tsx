import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";
import { mockStorageReport, mockStorageSettings } from "@/mocks/data/storage";

vi.mock("@/api/admin/hooks", () => ({
  useStorageSettings: vi.fn(),
  useSetStorageSettings: vi.fn(),
  useStorageReport: vi.fn(),
  useNotificationChannels: vi.fn(),
}));

import { useNotificationChannels, useSetStorageSettings, useStorageReport, useStorageSettings } from "@/api/admin/hooks";
import { StorageSettingsCard } from "./StorageSettingsCard";

const mutate = vi.fn();
const GIB = 1024 ** 3;

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(useStorageSettings).mockReturnValue({
    data: structuredClone(mockStorageSettings),
    isLoading: false,
  } as unknown as ReturnType<typeof useStorageSettings>);
  vi.mocked(useStorageReport).mockReturnValue({ data: mockStorageReport(), isLoading: false } as unknown as ReturnType<
    typeof useStorageReport
  >);
  vi.mocked(useSetStorageSettings).mockReturnValue({ mutate, isPending: false } as unknown as ReturnType<
    typeof useSetStorageSettings
  >);
  vi.mocked(useNotificationChannels).mockReturnValue({
    data: { channels: [{ name: "ops-alerts", enabled: true }], kinds: [] },
    isLoading: false,
  } as unknown as ReturnType<typeof useNotificationChannels>);
});

afterEach(cleanup);

describe("StorageSettingsCard (#2148)", () => {
  it("shows the stored settings in GiB and saves an edit in bytes", () => {
    render(<StorageSettingsCard isReadOnly={false} />);
    expect(screen.getByLabelText("Database limit (GiB)")).toHaveValue("100");
    expect(screen.getByLabelText("SeaweedFS master")).toHaveValue("http://seaweedfs-master:9333");

    fireEvent.change(screen.getByLabelText("Database limit (GiB)"), { target: { value: "250" } });
    fireEvent.change(screen.getByLabelText("Used % warning"), { target: { value: "70" } });
    fireEvent.click(screen.getByRole("button", { name: /save/i }));

    const input = mutate.mock.calls[0]![0];
    expect(input.database_limit_bytes).toBe(250 * GIB);
    expect(input.used_percent).toEqual({ warning: 70, critical: 90 });
    expect(input).not.toHaveProperty("updated_by");
  });

  it("adds and removes a bucket budget", () => {
    render(<StorageSettingsCard isReadOnly={false} />);
    fireEvent.click(screen.getByRole("button", { name: /Add budget/ }));
    const buckets = screen.getAllByLabelText("Bucket");
    fireEvent.change(buckets[1]!, { target: { value: "acme-resources" } });
    fireEvent.change(screen.getAllByLabelText("Budget (GiB)")[1]!, { target: { value: "2" } });
    fireEvent.click(screen.getAllByRole("button", { name: "Remove budget" })[0]!);
    fireEvent.click(screen.getByRole("button", { name: /save/i }));
    expect(mutate.mock.calls[0]![0].budgets).toEqual({ "acme-resources": 2 * GIB });
  });

  it("reads the volume servers one per line", () => {
    render(<StorageSettingsCard isReadOnly={false} />);
    fireEvent.change(screen.getByLabelText("SeaweedFS volume servers"), {
      target: { value: "http://v1:8080\nhttp://v2:8080" },
    });
    fireEvent.click(screen.getByRole("button", { name: /save/i }));
    expect(mutate.mock.calls[0]![0].seaweedfs.volume_urls).toEqual(["http://v1:8080", "http://v2:8080"]);
  });

  it("offers the SeaweedFS fields only where a bucket is on SeaweedFS or an address is stored", () => {
    const report = mockStorageReport();
    report.seaweedfs = null;
    report.seaweedfs_buckets = [];
    vi.mocked(useStorageReport).mockReturnValue({ data: report, isLoading: false } as unknown as ReturnType<
      typeof useStorageReport
    >);
    const settings = structuredClone(mockStorageSettings);
    settings.seaweedfs = { master_url: "", volume_urls: [] };
    vi.mocked(useStorageSettings).mockReturnValue({ data: settings, isLoading: false } as unknown as ReturnType<
      typeof useStorageSettings
    >);
    const { container, rerender } = render(<StorageSettingsCard isReadOnly={false} />);
    expect(screen.queryByLabelText("SeaweedFS master")).toBeNull();
    expect(container.textContent).not.toMatch(/SeaweedFS/);

    report.seaweedfs_buckets = ["acme-portal"];
    rerender(<StorageSettingsCard isReadOnly={false} />);
    expect(screen.getByLabelText("SeaweedFS master")).toHaveValue("");
    expect(screen.getByLabelText("SeaweedFS volume servers")).toBeInTheDocument();

    report.seaweedfs_buckets = [];
    settings.seaweedfs.master_url = "http://seaweedfs-master:9333";
    rerender(<StorageSettingsCard isReadOnly={false} />);
    expect(screen.getByLabelText("SeaweedFS master")).toHaveValue("http://seaweedfs-master:9333");
  });

  it("offers no edit in file config mode", () => {
    render(<StorageSettingsCard isReadOnly />);
    expect(screen.getByLabelText("Database limit (GiB)")).toBeDisabled();
    expect(screen.queryByRole("button", { name: /Add budget/ })).toBeNull();
    expect(screen.queryByRole("button", { name: /save/i })).toBeNull();
  });

  it("says it is loading before the settings arrive", () => {
    vi.mocked(useStorageSettings).mockReturnValue({ data: undefined, isLoading: true } as unknown as ReturnType<
      typeof useStorageSettings
    >);
    render(<StorageSettingsCard isReadOnly={false} />);
    expect(screen.getByText("Loading...")).toBeInTheDocument();
  });
});
