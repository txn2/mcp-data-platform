import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";

const reads: Array<{ rowStart?: number; rowEnd?: number }> = [];

vi.mock("hyparquet", () => ({
  asyncBufferFromUrl: vi.fn(async ({ byteLength }: { byteLength?: number }) => ({
    byteLength: byteLength ?? 4096,
    slice: vi.fn(),
  })),
  parquetMetadataAsync: vi.fn(async () => ({
    version: 2,
    num_rows: 3n,
    created_by: "parquet-cpp-arrow version 17.0.0",
    metadata_length: 100,
    schema: [
      { name: "schema", num_children: 2 },
      { name: "id", type: "INT64", repetition_type: "OPTIONAL" },
      { name: "t", type: "INT64", repetition_type: "OPTIONAL", logical_type: { type: "TIME", isAdjustedToUTC: false, unit: "MICROS" } },
    ],
    row_groups: [
      { num_rows: 2n, total_byte_size: 10n, columns: [{ meta_data: { codec: "ZSTD" } }] },
      { num_rows: 1n, total_byte_size: 10n, columns: [{ meta_data: { codec: "ZSTD" } }] },
    ],
  })),
  parquetReadObjects: vi.fn(async (opts: { rowStart?: number; rowEnd?: number }) => {
    reads.push({ rowStart: opts.rowStart, rowEnd: opts.rowEnd });
    return opts.rowStart === 0
      ? [
          { id: 1n, t: 5n },
          { id: 2n, t: 6n },
        ]
      : [{ id: 3n, t: 7n }];
  }),
}));

vi.mock("./parquet/decompressors", () => ({ decompressors: {} }));

import { ParquetRenderer } from "./ParquetRenderer";

beforeEach(() => {
  reads.length = 0;
});
afterEach(cleanup);

describe("ParquetRenderer", () => {
  it("shows the schema with its registration types, the file facts, and the first row group", async () => {
    render(<ParquetRenderer contentUrl="/content/orders.parquet" fileName="orders.parquet" sizeBytes={2048} />);

    const schema = await screen.findByTestId("parquet-schema");
    expect(within(schema).getByText("BIGINT")).toBeInTheDocument();
    expect(within(schema).getByText(/not registrable/)).toBeInTheDocument();

    const facts = screen.getByTestId("parquet-facts");
    expect(within(facts).getByText("3")).toBeInTheDocument();
    expect(within(facts).getByText("2")).toBeInTheDocument();
    expect(within(facts).getByText("ZSTD")).toBeInTheDocument();
    expect(within(facts).getByText("parquet-cpp-arrow version 17.0.0")).toBeInTheDocument();

    await waitFor(() => expect(screen.getAllByRole("button", { name: /^Open row/ })).toHaveLength(2));
    expect(reads[0]).toEqual({ rowStart: 0, rowEnd: 2 });
    expect(screen.getByRole("link", { name: /Download/ })).toHaveAttribute("href", "/content/orders.parquet");
  });

  it("reads a later row group when paged to it, and only that group", async () => {
    render(<ParquetRenderer contentUrl="/content/orders.parquet" />);
    await screen.findByText("Row group 1 of 2");
    fireEvent.click(screen.getByRole("button", { name: "Next row group" }));
    await screen.findByText("Row group 2 of 2");
    await waitFor(() => expect(screen.getAllByRole("button", { name: /^Open row/ })).toHaveLength(1));
    expect(reads[reads.length - 1]).toEqual({ rowStart: 2, rowEnd: 3 });
    expect(screen.getByRole("button", { name: "Next row group" })).toBeDisabled();
  });

  it("says why a file cannot be read, and still offers the download", async () => {
    const hyparquet = await import("hyparquet");
    vi.mocked(hyparquet.parquetMetadataAsync).mockRejectedValueOnce(new Error("parquet file invalid"));
    render(<ParquetRenderer contentUrl="/content/bad.parquet" />);
    expect(await screen.findByText(/could not be read: parquet file invalid/)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /Download/ })).toBeInTheDocument();
  });
});
