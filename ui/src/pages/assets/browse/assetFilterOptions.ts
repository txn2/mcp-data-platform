import { LayoutGrid, List } from "lucide-react";

/** The content types assets are actually saved as, as a filter facet. */
export const CONTENT_TYPES = [
  { value: "", label: "All types" },
  { value: "text/html", label: "HTML" },
  { value: "text/jsx", label: "JSX" },
  { value: "image/svg+xml", label: "SVG" },
  { value: "text/markdown", label: "Markdown" },
  { value: "text/csv", label: "CSV" },
];

/** The Status filter's choices (#2502); "" is any status or none. */
export const STATUS_OPTIONS = [
  { value: "", label: "Any status" },
  { value: "none", label: "No status" },
  { value: "draft", label: "Draft" },
  { value: "published", label: "Published" },
  { value: "deprecated", label: "Deprecated" },
];

export const VIEW_OPTIONS = [
  { value: "grid" as const, label: "Grid view", icon: LayoutGrid },
  { value: "table" as const, label: "Table view", icon: List },
];

/** The label an option list gives a value, the value itself when it has none. */
export function labelOf(options: { value: string; label: string }[], value: string): string {
  return options.find((o) => o.value === value)?.label ?? value;
}
