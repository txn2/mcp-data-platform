import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { cn } from "@/lib/utils";

// A Radix listbox item cannot carry an empty value, but "" is how every facet
// here says "no filter". The unfiltered choice travels under this sentinel and
// is translated back at this boundary, so no caller knows about it.
const ALL = "__all__";

export interface FilterOption {
  value: string;
  label: string;
}

// FilterSelect is one facet of a filter bar: a compact listbox whose visible
// label is the choice itself ("All statuses", "failed"), named for assistive
// tech by `label` rather than by a floating caption. Filter bars are wide and
// horizontal, so the facets carry no visible labels; without this the control
// would be an unnamed combobox.
export function FilterSelect({
  label,
  value,
  onChange,
  options,
  title,
  disabled = false,
  className,
}: {
  // The accessible name of the control, e.g. "Filter by status".
  label: string;
  value: string;
  onChange: (value: string) => void;
  options: FilterOption[];
  // Hover copy explaining what the facet means, when the options alone do not.
  title?: string;
  // Set when the facet does not apply to what is on screen (e.g. a browse-only
  // filter while a relevance search is running), so it reads as inert rather
  // than as a filter that silently does nothing.
  disabled?: boolean;
  className?: string;
}) {
  return (
    <Select
      value={value === "" ? ALL : value}
      onValueChange={(v) => onChange(v === ALL ? "" : v)}
      disabled={disabled}
    >
      <SelectTrigger
        size="sm"
        aria-label={label}
        title={title}
        className={cn("text-xs", className)}
      >
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        {options.map((o) => (
          <SelectItem key={o.value} value={o.value === "" ? ALL : o.value}>
            {o.label}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}
