import { useMemo, useState } from "react";
import { EmptyState } from "@/components/patterns/EmptyState";
import { SearchInput } from "@/components/patterns/SearchInput";
import { cn } from "@/lib/utils";
import type { APIOperationSummary } from "@/api/apis/types";
import { MethodBadge } from "@/components/patterns/MethodBadge";

/** UNTAGGED is the group an operation with no tag falls into. */
const UNTAGGED = "Untagged";

export interface OperationSelection {
  operationID: string;
  spec: string;
}

interface OperationIndexProps {
  operations: APIOperationSummary[];
  selected: OperationSelection | null;
  onSelect: (selection: OperationSelection) => void;
  /** Rendered in place of the list when the source has no operations at all. */
  emptyMessage: string;
  loading?: boolean;
}

interface Group {
  key: string;
  spec: string;
  tag: string;
  operations: APIOperationSummary[];
}

/**
 * groupOperations arranges the index the way the documents themselves are
 * arranged: by component spec, then by the tag the spec's author grouped the
 * operation under. Both levels are sorted by name and the operations within a
 * group by path, so the index reads the same on every replica.
 *
 * An operation carrying several tags appears under each of them. That is what
 * the tag means in OpenAPI: a cross-cutting operation (a search tagged both
 * `users` and `orders`) is genuinely part of both sections, and showing it
 * under one of them arbitrarily would hide it from a reader browsing the other.
 */
export function groupOperations(operations: APIOperationSummary[]): Group[] {
  const groups = new Map<string, Group>();
  for (const op of operations) {
    const spec = op.spec ?? "";
    const tags = op.tags && op.tags.length > 0 ? op.tags : [UNTAGGED];
    for (const tag of tags) {
      const key = `${spec} ${tag}`;
      const group = groups.get(key) ?? { key, spec, tag, operations: [] };
      group.operations.push(op);
      groups.set(key, group);
    }
  }
  const out = [...groups.values()];
  for (const group of out) {
    group.operations.sort(
      (a, b) => a.path.localeCompare(b.path) || a.method.localeCompare(b.method),
    );
  }
  return out.sort((a, b) => a.spec.localeCompare(b.spec) || a.tag.localeCompare(b.tag));
}

/** matches is the free-text filter: operation id, path and summary. */
function matches(op: APIOperationSummary, query: string): boolean {
  return (
    op.operation_id.toLowerCase().includes(query) ||
    op.path.toLowerCase().includes(query) ||
    (op.summary ?? "").toLowerCase().includes(query)
  );
}

/**
 * OperationIndex is the left half of the browser: every operation the reader
 * reaches, grouped and filterable, with the selected one lit.
 */
export function OperationIndex({
  operations,
  selected,
  onSelect,
  emptyMessage,
  loading,
}: OperationIndexProps) {
  const [search, setSearch] = useState("");

  const query = search.trim().toLowerCase();
  const filtered = useMemo(
    () => (query ? operations.filter((op) => matches(op, query)) : operations),
    [operations, query],
  );
  const groups = useMemo(() => groupOperations(filtered), [filtered]);
  // Whether the source is empty is a different fact from whether the filter
  // matched nothing, and the two need different copy: one says what is missing,
  // the other says the search found none of it.
  const sourceIsEmpty = operations.length === 0;

  return (
    <div className="flex h-full flex-col">
      <div className="border-b p-3">
        <SearchInput
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder="Filter operations..."
          aria-label="Filter operations"
        />
      </div>

      <div className="flex-1 overflow-auto">
        {loading ? (
          <p className="p-6 text-center text-sm text-muted-foreground">Loading operations...</p>
        ) : groups.length === 0 ? (
          <EmptyState className="m-3">
            {sourceIsEmpty ? emptyMessage : `No operation matches "${search.trim()}".`}
          </EmptyState>
        ) : (
          groups.map((group) => (
            <div key={group.key}>
              <div className="sticky top-0 z-10 flex items-baseline gap-1 border-b bg-muted px-3 py-1 text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
                <span className="truncate">
                  {group.spec ? `${group.spec} / ` : ""}
                  {group.tag}
                </span>
                <span className="text-muted-foreground/70">({group.operations.length})</span>
              </div>
              {group.operations.map((op) => {
                // A deep link may carry the operation and not the spec, which
                // the detail route resolves without one. Match on the id alone
                // in that case, or the reader gets the operation open with
                // nothing in the index showing where they are.
                const isSelected =
                  selected?.operationID === op.operation_id &&
                  (selected.spec === "" || selected.spec === (op.spec ?? ""));
                return (
                  <button
                    key={`${group.key}-${op.method}-${op.path}`}
                    type="button"
                    onClick={() => onSelect({ operationID: op.operation_id, spec: op.spec ?? "" })}
                    className={cn(
                      "flex w-full items-start gap-2 border-b px-3 py-2 text-left text-sm transition-colors hover:bg-muted/40",
                      isSelected && "bg-primary/10 hover:bg-primary/15",
                    )}
                  >
                    <MethodBadge method={op.method} className="mt-0.5 w-14" />
                    <span className="min-w-0 flex-1">
                      <span className="block truncate font-mono text-[12px]">{op.path}</span>
                      {op.summary && (
                        <span className="block truncate text-[11px] text-muted-foreground">
                          {op.summary}
                        </span>
                      )}
                    </span>
                  </button>
                );
              })}
            </div>
          ))
        )}
      </div>
    </div>
  );
}
