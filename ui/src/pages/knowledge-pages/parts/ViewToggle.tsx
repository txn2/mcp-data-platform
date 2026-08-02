import { LayoutGrid, Share2 } from "lucide-react";

/** KnowledgeView is the layout the knowledge-pages surface is rendered in. */
export type KnowledgeView = "cards" | "graph";

const OPTIONS: { value: KnowledgeView; label: string; Icon: typeof LayoutGrid }[] = [
  { value: "cards", label: "Cards", Icon: LayoutGrid },
  { value: "graph", label: "Graph", Icon: Share2 },
];

/**
 * ViewToggle switches the knowledge corpus between the card list and the graph.
 * The two are alternate layouts of the same filtered corpus, so the control is a
 * radio group rather than a navigation: the reader's search and tag filter carry
 * across the switch.
 */
export function ViewToggle({
  value,
  onChange,
}: {
  value: KnowledgeView;
  onChange: (view: KnowledgeView) => void;
}) {
  return (
    <div role="radiogroup" aria-label="Knowledge layout" className="inline-flex rounded-md border border-border p-0.5">
      {OPTIONS.map(({ value: v, label, Icon }) => (
        <button
          key={v}
          type="button"
          role="radio"
          aria-checked={value === v}
          onClick={() => onChange(v)}
          className={`inline-flex items-center gap-1.5 rounded px-2.5 py-1 text-sm font-medium transition-colors ${
            value === v
              ? "bg-primary/10 text-primary"
              : "text-muted-foreground hover:bg-muted"
          }`}
        >
          <Icon className="h-4 w-4" /> {label}
        </button>
      ))}
    </div>
  );
}
