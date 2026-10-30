import type { Compressors } from "hyparquet";
import { decompress as decompressZstd } from "fzstd";
import { gunzip } from "hyparquet-compressors/src/gzip.js";
import { decompressBrotli } from "hyparquet-compressors/src/brotli.js";
import { decompressLz4, decompressLz4Raw } from "hyparquet-compressors/src/lz4.js";

/**
 * The decompressors the Parquet viewer reads a file with (#1833): ZSTD, GZIP,
 * Brotli and LZ4. Snappy is hyparquet's own.
 *
 * Imported one by one rather than through hyparquet-compressors' index, whose
 * `compressors` compiles a WebAssembly Snappy decoder the moment the module is
 * evaluated. That compile is a cost every Parquet preview would pay for a codec
 * hyparquet already decodes in plain JavaScript, and it throws wherever a page
 * refuses WebAssembly, taking the whole viewer chunk with it.
 */
export const decompressors: Compressors = {
  GZIP: (input, length) => gunzip(input, new Uint8Array(length)),
  BROTLI: decompressBrotli,
  ZSTD: (input) => decompressZstd(input),
  LZ4: decompressLz4,
  LZ4_RAW: decompressLz4Raw,
};
