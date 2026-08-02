import type { GraphMode } from "./useGraphFocus";
import type { GraphAnalysis } from "./useGraphAnalysis";

interface GraphSummaryProps {
  analysis: GraphAnalysis;
  visibleNodes: number;
  visibleEdges: number;
  corpusNodes: number;
  mode: GraphMode;
  focusLabel?: string;
  /** Matches across the whole loaded corpus. */
  matchCount: number;
  /** How many of those are inside the drawn neighbourhood. */
  matchesInView: number;
  queryText: string;
}

/**
 * GraphSummary states what is on screen and what the analysis found. The numbers
 * behind the picture are written out rather than left implied by node sizes: a
 * reader should be able to tell whether the corpus really has cluster structure
 * without inferring it from a drawing.
 */
export function GraphSummary({
  analysis,
  visibleNodes,
  visibleEdges,
  corpusNodes,
  mode,
  focusLabel,
  matchCount,
  matchesInView,
  queryText,
}: GraphSummaryProps) {
  return (
    <p className="text-xs text-muted-foreground">
      {mode === "focus" && focusLabel ? (
        <>
          Around <span className="font-medium text-foreground">{focusLabel}</span>: {visibleNodes} of{" "}
          {corpusNodes} nodes
        </>
      ) : (
        <>
          {visibleNodes} nodes, {visibleEdges} references
        </>
      )}
      {analysis.communityCount > 1 &&
        ` · ${analysis.communityCount} clusters (modularity ${analysis.modularity.toFixed(2)})`}
      {analysis.degraded && " · corpus too large to rank bridges; sized by connections"}
      {queryText && (
        <>
          {` · ${matchCount} matching "${queryText}"`}
          {/* Say when matches exist that this view is not showing, so the count
              is not read as "nothing here matches". */}
          {matchesInView < matchCount &&
            ` (${matchCount - matchesInView} outside this view)`}
        </>
      )}
    </p>
  );
}
