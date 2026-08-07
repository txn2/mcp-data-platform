import { X } from "lucide-react";
import type { PromptCollection } from "@/api/admin/types";
import { Button } from "@/components/ui/button";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import type { UsageFacet } from "./promptUsage";
import { allFacets, facetsActive, type Facets } from "./promptList";

// PromptFacetsBar narrows the library by collection, tag, status (My
// Prompts), owner (Library), and usage (#1010). Rendered in browse mode only;
// search results keep their rank order.

// A Select item cannot carry an empty value, but "no filter" is exactly that in
// the facet model, so the unfiltered choice travels under a sentinel and is
// translated back at this boundary.
const ANY = "__any__";

interface Option {
  value: string;
  label: string;
}

function FacetSelect({
  label,
  value,
  options,
  onChange,
}: {
  label: string;
  value: string;
  options: Option[];
  onChange: (value: string) => void;
}) {
  return (
    <Select value={value || ANY} onValueChange={(v) => onChange(v === ANY ? "" : v)}>
      <SelectTrigger size="sm" aria-label={label} className="text-xs">
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        {options.map((o) => (
          <SelectItem key={o.value || ANY} value={o.value || ANY} className="text-xs">
            {o.label}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}

interface Props {
  facets: Facets;
  onChange: (next: Facets) => void;
  collections: PromptCollection[];
  tagOptions: string[];
  ownerOptions: string[];
  isMineTab: boolean;
}

const STATUS_OPTIONS: Option[] = [
  { value: "", label: "All statuses" },
  { value: "draft", label: "Draft" },
  { value: "approved", label: "Approved" },
  { value: "deprecated", label: "Deprecated" },
  { value: "superseded", label: "Superseded" },
];

const USAGE_OPTIONS: Option[] = [
  { value: "all", label: "Any activity" },
  { value: "active", label: "Recently used" },
  { value: "inactive", label: "Never or long unused" },
];

export function PromptFacetsBar({ facets, onChange, collections, tagOptions, ownerOptions, isMineTab }: Props) {
  return (
    <div className="flex flex-wrap items-center gap-2" data-testid="prompt-facets">
      <FacetSelect
        label="Filter by collection"
        value={facets.collection}
        onChange={(collection) => onChange({ ...facets, collection })}
        options={[
          { value: "", label: "All collections" },
          { value: "none", label: "Uncollected" },
          ...collections.map((c) => ({ value: c.id, label: c.name })),
        ]}
      />
      <FacetSelect
        label="Filter by tag"
        value={facets.tag}
        onChange={(tag) => onChange({ ...facets, tag })}
        options={[{ value: "", label: "All tags" }, ...tagOptions.map((t) => ({ value: t, label: t }))]}
      />
      {isMineTab ? (
        <FacetSelect
          label="Filter by status"
          value={facets.status}
          onChange={(status) => onChange({ ...facets, status })}
          options={STATUS_OPTIONS}
        />
      ) : (
        <FacetSelect
          label="Filter by owner"
          value={facets.owner}
          onChange={(owner) => onChange({ ...facets, owner })}
          options={[
            { value: "", label: "All owners" },
            ...ownerOptions.map((o) => ({ value: o, label: o })),
          ]}
        />
      )}
      <FacetSelect
        label="Filter by usage"
        value={facets.usage}
        onChange={(usage) => onChange({ ...facets, usage: usage as UsageFacet })}
        options={USAGE_OPTIONS}
      />
      {facetsActive(facets) && (
        <Button variant="outline" size="sm" onClick={() => onChange(allFacets)}>
          <X /> Clear filters
        </Button>
      )}
    </div>
  );
}
