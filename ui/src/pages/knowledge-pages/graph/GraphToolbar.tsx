import { FilterChip } from "@/components/FilterChip";
import {
  SegmentedControl,
  type SegmentedOption,
} from "@/components/patterns/SegmentedControl";
import { typeLabel } from "./graphModel";
import { DEPTHS, type GraphMode } from "./useGraphFocus";

const SCOPES: SegmentedOption<GraphMode>[] = [
  { value: "focus", label: "Explore", text: "Explore" },
  { value: "corpus", label: "Whole corpus", text: "Whole corpus" },
];

// One face per hop count, named for what the number means so the control reads
// as a distance rather than an unlabelled index.
const HOPS: SegmentedOption<string>[] = DEPTHS.map((d) => ({
  value: String(d),
  label: `${d} hop${d === 1 ? "" : "s"}`,
  text: String(d),
}));

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
      <SegmentedControl
        label="Graph scope"
        value={mode}
        onChange={onModeChange}
        options={SCOPES}
      />

      {mode === "focus" && (
        <div className="flex items-center gap-1.5">
          <span className="text-xs font-medium tracking-wide text-muted-foreground uppercase">
            Hops
          </span>
          <SegmentedControl
            label="Neighbourhood depth"
            value={String(depth)}
            onChange={(d) => onDepthChange(Number(d))}
            options={HOPS}
          />
        </div>
      )}

      <div className="flex flex-wrap items-center gap-1.5">
        <span className="text-xs font-medium tracking-wide text-muted-foreground uppercase">
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
