import { X } from "lucide-react";
import type { PromptCollection } from "@/api/admin/types";
import type { UsageFacet } from "./promptUsage";
import { allFacets, facetsActive, type Facets } from "./promptList";

// PromptFacetsBar narrows the library by collection, tag, status (My
// Prompts), owner (Library), and usage (#1010). Rendered in browse mode only;
// search results keep their rank order.

interface Props {
  facets: Facets;
  onChange: (next: Facets) => void;
  collections: PromptCollection[];
  tagOptions: string[];
  ownerOptions: string[];
  isMineTab: boolean;
}

export function PromptFacetsBar({ facets, onChange, collections, tagOptions, ownerOptions, isMineTab }: Props) {
  return (
    <div className="flex flex-wrap items-center gap-2 text-xs" data-testid="prompt-facets">
      <select
        value={facets.collection}
        onChange={(e) => onChange({ ...facets, collection: e.target.value })}
        className="rounded-md border bg-background px-2 py-1.5 outline-none"
        aria-label="Filter by collection"
      >
        <option value="">All collections</option>
        <option value="none">Uncollected</option>
        {collections.map((c) => (
          <option key={c.id} value={c.id}>{c.name}</option>
        ))}
      </select>
      <select
        value={facets.tag}
        onChange={(e) => onChange({ ...facets, tag: e.target.value })}
        className="rounded-md border bg-background px-2 py-1.5 outline-none"
        aria-label="Filter by tag"
      >
        <option value="">All tags</option>
        {tagOptions.map((t) => <option key={t} value={t}>{t}</option>)}
      </select>
      {isMineTab ? (
        <select
          value={facets.status}
          onChange={(e) => onChange({ ...facets, status: e.target.value })}
          className="rounded-md border bg-background px-2 py-1.5 outline-none"
          aria-label="Filter by status"
        >
          <option value="">All statuses</option>
          <option value="draft">Draft</option>
          <option value="approved">Approved</option>
          <option value="deprecated">Deprecated</option>
          <option value="superseded">Superseded</option>
        </select>
      ) : (
        <select
          value={facets.owner}
          onChange={(e) => onChange({ ...facets, owner: e.target.value })}
          className="rounded-md border bg-background px-2 py-1.5 outline-none"
          aria-label="Filter by owner"
        >
          <option value="">All owners</option>
          {ownerOptions.map((o) => <option key={o} value={o}>{o}</option>)}
        </select>
      )}
      <select
        value={facets.usage}
        onChange={(e) => onChange({ ...facets, usage: e.target.value as UsageFacet })}
        className="rounded-md border bg-background px-2 py-1.5 outline-none"
        aria-label="Filter by usage"
      >
        <option value="all">Any activity</option>
        <option value="active">Recently used</option>
        <option value="inactive">Never or long unused</option>
      </select>
      {facetsActive(facets) && (
        <button
          onClick={() => onChange(allFacets)}
          className="inline-flex items-center gap-1 rounded-md border px-2 py-1.5 hover:bg-accent"
        >
          <X className="h-3 w-3" /> Clear filters
        </button>
      )}
    </div>
  );
}
