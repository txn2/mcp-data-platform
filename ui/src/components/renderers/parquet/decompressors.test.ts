import { describe, expect, it } from "vitest";
import { gzipSync, brotliCompressSync, zstdCompressSync } from "zlib";
import { decompressors } from "./decompressors";

// The Parquet viewer decodes a file's pages with these (#1833). Snappy is left
// to hyparquet's own JavaScript decoder, which is why it is absent here: the
// package's Snappy compiles WebAssembly, which the public page's policy forbids.
describe("decompressors", () => {
  const text = new TextEncoder().encode("parquet page bytes ".repeat(20));

  const decode = (b: Uint8Array | undefined) => new TextDecoder().decode(b);

  it("round-trips GZIP and Brotli", () => {
    const want = decode(text);
    expect(decode(decompressors.GZIP?.(new Uint8Array(gzipSync(text)), text.length))).toBe(want);
    expect(decode(decompressors.BROTLI?.(new Uint8Array(brotliCompressSync(text)), text.length))).toBe(want);
  });

  // ZSTD is the codec the platform's own writer uses (tableparquet.Write), so
  // every Parquet file trino_export and platform.export produce reaches the
  // viewer through this one. Asserting it is a function passes against a stub
  // that returns nothing.
  it("round-trips ZSTD, the codec the platform writes", () => {
    expect(decode(decompressors.ZSTD?.(new Uint8Array(zstdCompressSync(text)), text.length))).toBe(decode(text));
  });

  it("carries LZ4 and leaves Snappy to hyparquet", () => {
    expect(typeof decompressors.LZ4).toBe("function");
    expect(typeof decompressors.LZ4_RAW).toBe("function");
    expect(decompressors.SNAPPY).toBeUndefined();
  });
});
