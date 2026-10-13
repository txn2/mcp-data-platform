import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { mockCatalogScans, mockCatalogSettings } from "@/mocks/data/catalogScan";

const h = vi.hoisted(() => ({
  scans: { data: undefined as unknown, isLoading: false, isError: false },
  settings: { data: undefined as unknown, isError: false },
  request: { mutate: vi.fn(), isPending: false, isSuccess: false, isError: false },
  save: { mutate: vi.fn(), isPending: false, isSuccess: false, isError: false, error: null as unknown },
}));

vi.mock("@/api/admin/hooks", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/admin/hooks")>();
  return {
    ...actual,
    useCatalogScans: () => h.scans,
    useCatalogSettings: () => h.settings,
    useRequestCatalogScan: () => h.request,
    useSaveCatalogSettings: () => h.save,
    useConnectionInstances: () => ({ data: [] }),
  };
});

import { CatalogPage } from "./CatalogPage";

beforeEach(() => {
  h.scans = { data: { scans: mockCatalogScans }, isLoading: false, isError: false };
  h.settings = { data: mockCatalogSettings, isError: false };
  h.request = { mutate: vi.fn(), isPending: false, isSuccess: false, isError: false };
  h.save = { mutate: vi.fn(), isPending: false, isSuccess: false, isError: false, error: null };
});

describe("CatalogPage scans", () => {
  it("lists each connection's last scan and its problem", () => {
    render(<CatalogPage />);
    expect(screen.getByText("acme")).toBeInTheDocument();
    expect(screen.getByText("1,212")).toBeInTheDocument();
    expect(screen.getByText(/schema has not been read/)).toBeInTheDocument();
  });

  it("names each part a scan could not read, with the reason on hover", () => {
    render(<CatalogPage />);
    const unread = screen.getByText("Not read: legacy");
    expect(unread).toHaveAttribute("title", expect.stringContaining("Connection refused"));
  });

  it("requests a scan", () => {
    render(<CatalogPage />);
    fireEvent.click(screen.getByRole("button", { name: /Scan now/ }));
    expect(h.request.mutate).toHaveBeenCalledTimes(1);
  });

  it("says a requested scan is on its way", () => {
    h.request = { ...h.request, isSuccess: true };
    render(<CatalogPage />);
    expect(screen.getByText(/Scan requested/)).toBeInTheDocument();
  });

  it("says when nothing has been scanned", () => {
    h.scans = { data: { scans: [] }, isLoading: false, isError: false };
    render(<CatalogPage />);
    expect(screen.getByText("No connection has been scanned yet.")).toBeInTheDocument();
  });

  it("says when the scans cannot be read, and when a request failed", () => {
    h.scans = { data: undefined, isLoading: false, isError: true };
    h.request = { ...h.request, isError: true };
    render(<CatalogPage />);
    expect(screen.getByText("The scans could not be read.")).toBeInTheDocument();
    expect(screen.getByText("The scan could not be requested.")).toBeInTheDocument();
  });
});

describe("CatalogPage settings", () => {
  it("opens on the tab the path names and shows the stored settings", () => {
    render(<CatalogPage initialTab="settings" />);
    expect(screen.getByDisplayValue("6h")).toBeInTheDocument();
    expect(screen.getByDisplayValue("hive.tmp")).toBeInTheDocument();
  });

  it("saves an edited interval and a removed exclusion", () => {
    render(<CatalogPage initialTab="settings" />);
    fireEvent.change(screen.getByDisplayValue("6h"), { target: { value: "2h" } });
    fireEvent.click(screen.getByRole("button", { name: "Remove" }));
    expect(screen.getByText("Every connection is scanned in full.")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(h.save.mutate).toHaveBeenCalledWith({ scan_interval: "2h", exclusions: [] });
  });

  it("adds an exclusion row", () => {
    render(<CatalogPage initialTab="settings" />);
    fireEvent.click(screen.getByRole("button", { name: /Add exclusion/ }));
    expect(screen.getAllByText("Leave out")).toHaveLength(2);
  });

  it("reports a refused save and a saved one", () => {
    h.save = { ...h.save, isError: true, error: new Error("scan_interval must be at least 5m0s") };
    const { unmount } = render(<CatalogPage initialTab="settings" />);
    expect(screen.getByText("scan_interval must be at least 5m0s")).toBeInTheDocument();
    unmount();
    h.save = { ...h.save, isError: false, isSuccess: true };
    render(<CatalogPage initialTab="settings" />);
    expect(screen.getByText(/Saved. The next scan uses these settings./)).toBeInTheDocument();
  });

  it("says when the settings cannot be read", () => {
    h.settings = { data: undefined, isError: true };
    render(<CatalogPage initialTab="settings" />);
    expect(screen.getByText("The catalog settings could not be read.")).toBeInTheDocument();
  });
});
