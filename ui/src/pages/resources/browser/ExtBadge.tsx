import { cn } from "@/lib/utils";

/**
 * The colored extension mark a file row leads with (#1872), which is how a
 * file manager tells a CSV from a PDF at a glance. The colors are the POC's;
 * an extension without one is gray.
 */
const EXT: Record<string, [string, string]> = {
  pdf: ["PDF", "#c8453b"],
  md: ["MD", "#4f6a8f"],
  csv: ["CSV", "#2e8a57"],
  tsv: ["TSV", "#2e8a57"],
  xlsx: ["XLS", "#1f7a45"],
  xls: ["XLS", "#1f7a45"],
  png: ["PNG", "#8a55c4"],
  jpg: ["JPG", "#8a55c4"],
  jpeg: ["JPG", "#8a55c4"],
  gif: ["GIF", "#8a55c4"],
  webp: ["WEBP", "#8a55c4"],
  svg: ["SVG", "#b3702a"],
  json: ["JSON", "#6c7a1f"],
  jsonl: ["JSONL", "#6c7a1f"],
  html: ["HTML", "#c2572f"],
  txt: ["TXT", "#6b7280"],
  sql: ["SQL", "#2f6fae"],
  parquet: ["PQT", "#2f6fae"],
};

/** The extension of a filename, lowercased; empty when it has none. */
export function extOf(filename: string): string {
  const dot = filename.lastIndexOf(".");
  return dot > 0 ? filename.slice(dot + 1).toLowerCase() : "";
}

export function ExtBadge({ filename, className }: { filename: string; className?: string }) {
  const ext = extOf(filename);
  const [label, color] = EXT[ext] ?? [ext.toUpperCase().slice(0, 4) || "FILE", "#6b7280"];
  return (
    <span
      aria-hidden
      className={cn(
        "grid h-[18px] w-[30px] shrink-0 place-items-center rounded-[3px] font-mono text-[9.5px] font-semibold tracking-[.02em] text-white",
        className,
      )}
      style={{ background: color }}
    >
      {label}
    </span>
  );
}
