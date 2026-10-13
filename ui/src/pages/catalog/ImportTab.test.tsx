import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { mockCatalogImports } from "@/mocks/data/catalogScan";

const h = vi.hoisted(() => ({
  imports: { data: undefined as unknown, isLoading: false, isError: false },
  start: { mutate: vi.fn(), isPending: false, isSuccess: false, isError: false, error: null as unknown },
}));

vi.mock("@/api/admin/hooks", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/admin/hooks")>();
  return {
    ...actual,
    useCatalogImports: () => h.imports,
    useStartCatalogImport: () => h.start,
  };
});

import { ImportTab } from "./ImportTab";

beforeEach(() => {
  h.imports = { data: { imports: mockCatalogImports }, isLoading: false, isError: false };
  h.start = { mutate: vi.fn(), isPending: false, isSuccess: false, isError: false, error: null };
});

describe("ImportTab", () => {
  it("starts an import from the DataHub named, once both fields are filled", () => {
    render(<ImportTab />);
    const button = screen.getByRole("button", { name: /Import/ });
    expect(button).toBeDisabled();
    fireEvent.change(screen.getByLabelText(/DataHub URL/), { target: { value: " https://dh.example.com:8080 " } });
    fireEvent.change(screen.getByLabelText(/Access token/), { target: { value: "tok" } });
    fireEvent.click(button);
    expect(h.start.mutate).toHaveBeenCalledWith({ url: "https://dh.example.com:8080", token: "tok" });
  });

  it("shows the latest run's report and what it did not carry over", () => {
    render(<ImportTab />);
    expect(screen.getByText("Datasets imported")).toBeInTheDocument();
    expect(screen.getAllByText("1,118")).toHaveLength(2);
    expect(screen.getByText("Glossary terms")).toBeInTheDocument();
    expect(screen.getByText("Not carried over (139)")).toBeInTheDocument();
    expect(screen.getByText("The first 4 are listed.")).toBeInTheDocument();
    expect(screen.getByText("Tableau entities are not imported")).toBeInTheDocument();
  });

  it("opens a run's detail on a row click", () => {
    render(<ImportTab />);
    fireEvent.click(screen.getByText("https://datahub.example.com:9002"));
    expect(screen.getAllByText(/404 Not Found/).length).toBeGreaterThan(0);
    expect(screen.getByText("Failed")).toBeInTheDocument();
  });

  it("holds the button while a run is under way and says a start was accepted", () => {
    h.imports = {
      data: { imports: [{ ...mockCatalogImports[1], running: true, error: undefined, finished_at: undefined }] },
      isLoading: false,
      isError: false,
    };
    h.start = { ...h.start, isSuccess: true };
    render(<ImportTab />);
    fireEvent.change(screen.getByLabelText(/DataHub URL/), { target: { value: "https://dh" } });
    fireEvent.change(screen.getByLabelText(/Access token/), { target: { value: "t" } });
    expect(screen.getByRole("button", { name: /Import/ })).toBeDisabled();
    expect(screen.getByText("Running")).toBeInTheDocument();
    expect(screen.getByText("Import started.")).toBeInTheDocument();
  });

  it("reports a start the server refused", () => {
    h.start = { ...h.start, isError: true, error: new Error("an import from DataHub is already running") };
    render(<ImportTab />);
    expect(screen.getByText("an import from DataHub is already running")).toBeInTheDocument();
  });

  it("says when nothing has been imported, and when the runs cannot be read", () => {
    h.imports = { data: { imports: [] }, isLoading: false, isError: false };
    const { unmount } = render(<ImportTab />);
    expect(screen.getByText("Nothing has been imported yet.")).toBeInTheDocument();
    unmount();
    h.imports = { data: undefined, isLoading: false, isError: true };
    render(<ImportTab />);
    expect(screen.getByText("The imports could not be read.")).toBeInTheDocument();
  });

  it("names a run that stopped without finishing", () => {
    h.imports = {
      data: { imports: [{ ...mockCatalogImports[1], error: undefined, finished_at: undefined }] },
      isLoading: false,
      isError: false,
    };
    render(<ImportTab />);
    expect(screen.getByText("Stopped")).toBeInTheDocument();
  });
});
