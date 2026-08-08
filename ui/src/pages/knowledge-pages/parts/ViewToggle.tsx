import { LayoutGrid, Share2 } from "lucide-react";
import {
  SegmentedControl,
  type SegmentedOption,
} from "@/components/patterns/SegmentedControl";

/** KnowledgeView is the layout the knowledge-pages surface is rendered in. */
export type KnowledgeView = "cards" | "graph";

const OPTIONS: SegmentedOption<KnowledgeView>[] = [
  { value: "cards", label: "Cards", icon: LayoutGrid, text: "Cards" },
  { value: "graph", label: "Graph", icon: Share2, text: "Graph" },
];

/**
 * ViewToggle switches the knowledge corpus between the card list and the graph.
 * The two are alternate layouts of the same filtered corpus, so the control is a
 * SegmentedControl rather than a navigation: the reader's search and tag filter
 * carry across the switch, and nothing under it is a tab panel.
 */
export function ViewToggle({
  value,
  onChange,
}: {
  value: KnowledgeView;
  onChange: (view: KnowledgeView) => void;
}) {
  return (
    <SegmentedControl
      label="Knowledge layout"
      value={value}
      onChange={onChange}
      options={OPTIONS}
    />
  );
}
