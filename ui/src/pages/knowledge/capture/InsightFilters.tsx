import { FilterSelect } from "@/components/patterns/FilterSelect";
import {
  INSIGHT_CATEGORIES,
  INSIGHT_CONFIDENCES,
  INSIGHT_STATUSES,
  formatCategory,
} from "./helpers";

const STATUS_OPTIONS = [
  { value: "", label: "All Statuses" },
  ...INSIGHT_STATUSES.map((s) => ({ value: s, label: formatCategory(s) })),
];

const CATEGORY_OPTIONS = [
  { value: "", label: "All Categories" },
  ...INSIGHT_CATEGORIES.map((c) => ({ value: c, label: formatCategory(c) })),
];

const CONFIDENCE_OPTIONS = [
  { value: "", label: "All Confidence" },
  ...INSIGHT_CONFIDENCES.map((c) => ({ value: c, label: formatCategory(c) })),
];

const ORDER_OPTIONS = [
  { value: "newest", label: "Newest First" },
  { value: "oldest", label: "Oldest First" },
];

export type InsightOrder = "newest" | "oldest";

// InsightFilters is the review queue's facet bar: what state an insight is in,
// what it is about, how sure the capture was, and which end of the queue to read
// from. Every facet resets the page, which is the caller's job.
export function InsightFilters({
  statusFilter,
  categoryFilter,
  confidenceFilter,
  order,
  onStatusChange,
  onCategoryChange,
  onConfidenceChange,
  onOrderChange,
}: {
  statusFilter: string;
  categoryFilter: string;
  confidenceFilter: string;
  order: InsightOrder;
  onStatusChange: (v: string) => void;
  onCategoryChange: (v: string) => void;
  onConfidenceChange: (v: string) => void;
  onOrderChange: (v: InsightOrder) => void;
}) {
  return (
    <div className="flex flex-wrap items-center gap-3">
      <FilterSelect
        label="Filter by status"
        value={statusFilter}
        onChange={onStatusChange}
        options={STATUS_OPTIONS}
      />
      <FilterSelect
        label="Filter by category"
        value={categoryFilter}
        onChange={onCategoryChange}
        options={CATEGORY_OPTIONS}
      />
      <FilterSelect
        label="Filter by confidence"
        value={confidenceFilter}
        onChange={onConfidenceChange}
        options={CONFIDENCE_OPTIONS}
      />
      <FilterSelect
        label="Sort by age"
        value={order}
        onChange={(v) => onOrderChange(v as InsightOrder)}
        options={ORDER_OPTIONS}
      />
    </div>
  );
}
