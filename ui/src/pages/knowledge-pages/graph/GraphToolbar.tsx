import { FilterChip } from "@/components/FilterChip";
import { typeLabel } from "./graphModel";
import { DEPTHS, type GraphMode } from "./useGraphFocus";

interface GraphToolbarProps {
  /** Node types present, with counts, most-used first. */
  facet: [string, number][];
  hiddenTypes: Set<string>;
  onToggleType: (type: string) => void;
  mode: GraphMode;
  onModeChange: (mode: GraphMode) => void;
  depth: number;
  onDepthChange: (depth: number) => void;
}

/**
 * GraphToolbar carries what the reader is looking AT: the neighbourhood radius
 * around the focus node, whether to drop back to the whole corpus, and which
 * node types are in play. Keeping these out of the canvas means no control ever
 * covers a node.
 */
export function GraphToolbar({
  facet,
  hiddenTypes,
  onToggleType,
  mode,
  onModeChange,
  depth,
  onDepthChange,
}: GraphToolbarProps) {
  return (
    <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
      <div
        role="radiogroup"
        aria-label="Graph scope"
        className="inline-flex rounded-md border border-border p-0.5"
      >
        <ScopeOption label="Explore" active={mode === "focus"} onClick={() => onModeChange("focus")} />
        <ScopeOption label="Whole corpus" active={mode === "corpus"} onClick={() => onModeChange("corpus")} />
      </div>

      {mode === "focus" && (
        <div className="flex items-center gap-1.5">
          <span className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
            Hops
          </span>
          <div role="radiogroup" aria-label="Neighbourhood depth" className="inline-flex gap-1">
            {DEPTHS.map((d) => (
              <button
                key={d}
                type="button"
                role="radio"
                aria-checked={depth === d}
                onClick={() => onDepthChange(d)}
                className={`h-6 w-6 rounded-md border text-xs font-medium transition-colors ${
                  depth === d
                    ? "border-primary bg-primary/10 text-primary"
                    : "border-border text-muted-foreground hover:bg-muted"
                }`}
              >
                {d}
              </button>
            ))}
          </div>
        </div>
      )}

      <div className="flex flex-wrap items-center gap-1.5">
        <span className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
          Show
        </span>
        <div role="group" aria-label="Node types" className="flex flex-wrap items-center gap-1.5">
          {facet.map(([type, count]) => (
            <FilterChip
              key={type}
              label={typeLabel(type)}
              count={count}
              active={!hiddenTypes.has(type)}
              onClick={() => onToggleType(type)}
            />
          ))}
        </div>
      </div>
    </div>
  );
}

/** ScopeOption is one segment of the explore/whole-corpus switch. */
function ScopeOption({
  label,
  active,
  onClick,
}: {
  label: string;
  active: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      role="radio"
      aria-checked={active}
      onClick={onClick}
      className={`rounded px-2.5 py-1 text-sm font-medium transition-colors ${
        active ? "bg-primary/10 text-primary" : "text-muted-foreground hover:bg-muted"
      }`}
    >
      {label}
    </button>
  );
}
