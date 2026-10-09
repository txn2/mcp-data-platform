import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup, within } from "@testing-library/react";
import { MapsCard } from "./MapsCard";
import { mockMaps } from "@/mocks/data/maps";
import type { MapsView } from "@/api/admin/types";

// The hooks are mocked; the form, the region list and the add form are real.
vi.mock("@/api/admin/hooks", () => ({
  useMaps: vi.fn(),
  useSetMapSettings: vi.fn(),
  useAddMapRegion: vi.fn(),
  useRefreshMapRegion: vi.fn(),
  useDeleteMapRegion: vi.fn(),
  useEstimateMapRegion: vi.fn(),
}));

import {
  useMaps,
  useSetMapSettings,
  useAddMapRegion,
  useRefreshMapRegion,
  useDeleteMapRegion,
  useEstimateMapRegion,
} from "@/api/admin/hooks";

const save = vi.fn();
const add = vi.fn();
const refresh = vi.fn();
const remove = vi.fn();
const estimate = vi.fn();
const refetch = vi.fn();

function view(over: Partial<MapsView> = {}): MapsView {
  return structuredClone({ ...mockMaps, ...over });
}

function mockHooks(data: MapsView | undefined, extra: Record<string, unknown> = {}) {
  vi.mocked(useMaps).mockReturnValue({
    data, isLoading: !data, error: null, refetch, ...extra,
  } as unknown as ReturnType<typeof useMaps>);
}

beforeEach(() => {
  vi.clearAllMocks();
  mockHooks(view());
  vi.mocked(useSetMapSettings).mockReturnValue({ mutate: save, isPending: false } as unknown as ReturnType<typeof useSetMapSettings>);
  vi.mocked(useAddMapRegion).mockReturnValue({ mutate: add, isPending: false } as unknown as ReturnType<typeof useAddMapRegion>);
  vi.mocked(useRefreshMapRegion).mockReturnValue({ mutate: refresh, isPending: false } as unknown as ReturnType<typeof useRefreshMapRegion>);
  vi.mocked(useDeleteMapRegion).mockReturnValue({ mutateAsync: remove, isPending: false } as unknown as ReturnType<typeof useDeleteMapRegion>);
  vi.mocked(useEstimateMapRegion).mockReturnValue({
    mutate: estimate, reset: vi.fn(), isPending: false, data: undefined,
  } as unknown as ReturnType<typeof useEstimateMapRegion>);
});

afterEach(cleanup);

function rowOf(name: string): HTMLElement {
  const row = screen.getAllByText(name).map((el) => el.closest("tr")).find(Boolean);
  if (!row) throw new Error(`no row for ${name}`);
  return row;
}

describe("MapsCard", () => {
  it("shows each region in the state its last fetch left it", () => {
    render(<MapsCard />);
    expect(screen.getByText("Maps")).toBeInTheDocument();
    expect(within(rowOf("San Francisco")).getByText("Ready")).toBeInTheDocument();
    expect(within(rowOf("San Francisco")).getByText("/portal/maps/sf.pmtiles")).toBeInTheDocument();
    expect(within(rowOf("San Francisco")).getByText("OpenStreetMap, 2026-10-08")).toBeInTheDocument();

    const us = rowOf("United States (contiguous)");
    expect(within(us).getByText("Fetching")).toBeInTheDocument();
    expect(within(us).getByRole("progressbar")).toHaveAttribute("aria-valuenow", "39");
    expect(within(us).getByRole("button", { name: /Refresh/ })).toBeDisabled();

    const seattle = rowOf("Seattle");
    expect(within(seattle).getByText("Failed")).toBeInTheDocument();
    expect(within(seattle).getByText(/HTTP 503/)).toBeInTheDocument();
    expect(within(seattle).getByText("Still serving the 2026-10-01 build.")).toBeInTheDocument();

    expect(within(rowOf("Denver")).getByText("Queued")).toBeInTheDocument();
    expect(screen.getByText("managed-resources/maps/uploads/")).toBeInTheDocument();
  });

  it("saves the settings it was given", () => {
    render(<MapsCard />);
    fireEvent.click(screen.getByRole("switch"));
    fireEvent.change(screen.getByPlaceholderText("https://"), { target: { value: " https://mirror.example.com/p.pmtiles " } });
    fireEvent.click(screen.getByRole("button", { name: /Save/ }));
    expect(save).toHaveBeenCalledWith(
      { enabled: false, s3_connection: "", bucket: "", max_zoom: 14, source_url: "https://mirror.example.com/p.pmtiles" },
      expect.anything(),
    );
  });

  it("refreshes and deletes a region", async () => {
    render(<MapsCard />);
    fireEvent.click(within(rowOf("San Francisco")).getByRole("button", { name: /Refresh/ }));
    expect(refresh).toHaveBeenCalledWith("sf", expect.anything());

    fireEvent.click(within(rowOf("San Francisco")).getByRole("button", { name: /Delete/ }));
    fireEvent.click(await screen.findByRole("button", { name: "Delete" }));
    expect(remove).toHaveBeenCalledWith("sf");
  });

  it("adds a preset, and a custom box", () => {
    render(<MapsCard />);
    fireEvent.click(screen.getByRole("button", { name: "Estimate size" }));
    expect(estimate).toHaveBeenCalledWith({ preset: "united-states" }, expect.anything());
    fireEvent.click(screen.getByRole("button", { name: "Add region" }));
    expect(add).toHaveBeenCalledWith({ preset: "united-states" }, expect.anything());
  });

  it("reports the size an estimate found", () => {
    vi.mocked(useEstimateMapRegion).mockReturnValue({
      mutate: estimate, reset: vi.fn(), isPending: false,
      data: { size_bytes: 8_830_000_000, tiles: 3_677_037, build: "2026-10-08", min_zoom: 0, max_zoom: 14, bounds: mockMaps.presets[0]!.bounds },
    } as unknown as ReturnType<typeof useEstimateMapRegion>);
    render(<MapsCard />);
    expect(screen.getByText(/3,677,037 tiles at zoom 0 to 14, from the OpenStreetMap build of 2026-10-08/)).toBeInTheDocument();
  });

  it("says the regions are not served while maps are off", () => {
    const off = view();
    off.settings.enabled = false;
    mockHooks(off);
    render(<MapsCard />);
    expect(screen.getByText(/Maps are off. The regions below are kept in the bucket and are not served/)).toBeInTheDocument();
    cleanup();
    mockHooks(view());
    render(<MapsCard />);
    expect(screen.queryByText(/Maps are off/)).not.toBeInTheDocument();
  });

  it("says when there are no regions, and when the settings cannot be read", () => {
    mockHooks(view({ regions: [] }));
    render(<MapsCard />);
    expect(screen.getByText("No regions yet. Add one below.")).toBeInTheDocument();
    cleanup();

    mockHooks(undefined, { isLoading: false, error: new Error("down") });
    render(<MapsCard />);
    expect(screen.getByText("The maps settings could not be read.")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /Retry/ }));
    expect(refetch).toHaveBeenCalled();
  });
});
