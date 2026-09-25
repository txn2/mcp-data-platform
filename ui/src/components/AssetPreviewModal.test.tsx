import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { AssetPreviewModal } from "./AssetPreviewModal";

const FIVE_MB = 5 * 1024 * 1024;
const CSV = "region,total\nwest,4\n";

function open(sizeBytes: number, contentType = "text/csv") {
  return render(
    <AssetPreviewModal assetId="a1" assetName="data.csv" contentType={contentType} sizeBytes={sizeBytes} onClose={() => {}} />,
  );
}

describe("AssetPreviewModal", () => {
  afterEach(() => vi.unstubAllGlobals());

  // #1874: the modal refused anything over a flat 2 MB, so a table the asset
  // page shows was "too large to preview" here.
  it("renders a 5 MB CSV in the table viewer", async () => {
    const fetchMock = vi.fn(() => Promise.resolve(new Response(CSV, { status: 200 })));
    vi.stubGlobal("fetch", fetchMock);
    open(FIVE_MB);
    expect(await screen.findByText("west")).toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("offers a CSV past its family's limit as a download without requesting it", () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    open(40 * 1024 * 1024);
    expect(screen.getByText(/Too large to preview/)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /Download/ })).toHaveAttribute("href", "/api/v1/portal/assets/a1/content");
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("names a failed read's status and reads again on Retry", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(new Response("", { status: 500 }))
      .mockResolvedValueOnce(new Response(CSV, { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);
    open(12);
    expect(await screen.findByText(/HTTP 500/)).toBeInTheDocument();
    expect(screen.queryByText("Loading...")).not.toBeInTheDocument();
    expect(screen.getByRole("link", { name: /Download/ })).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /Retry/ }));
    expect(await screen.findByText("west")).toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });
});
