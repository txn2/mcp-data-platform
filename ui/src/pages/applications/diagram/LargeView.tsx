import { useMemo, useState } from "react";
import type { ApplicationDiagram } from "@/api/portal/hooks/applications";
import { Button } from "@/components/ui/button";
import { useIsMobile } from "@/hooks/useIsMobile";
import { DiagramTableView } from "./DiagramTableView";
import { reachOf, relatedTo, unwrittenTables, type DiagramView } from "./diagramScale";
import { LargeDiagram } from "./LargeDiagram";
import { largeLayout } from "./largeLayout";
import { LargePanel } from "./LargePanel";

// LargeView is a large application's Diagram tab (#2700): the drawing or the
// table, switched above them, and the side column that is always there. A
// phone opens on the table, as a list.

interface Props {
  diagram: ApplicationDiagram;
  selected: string | null;
  onSelect: (id: string | null) => void;
  onNavigate: (path: string) => void;
}

export function LargeView({ diagram, selected, onSelect, onNavigate }: Props) {
  const phone = useIsMobile();
  const [view, setView] = useState<DiagramView>(() => (phone ? "table" : "drawing"));
  const reach = useMemo(() => reachOf(diagram), [diagram]);
  const unwritten = useMemo(() => unwrittenTables(diagram.findings), [diagram]);
  const schema = diagram.nodes.find((n) => n.kind === "schema")?.label;
  const layout = useMemo(() => largeLayout(diagram, schema ?? ""), [diagram, schema]);
  const related = useMemo(() => relatedTo(diagram, reach, selected), [diagram, reach, selected]);
  const node = diagram.nodes.find((n) => n.id === selected) ?? null;
  const panel = !(phone && view === "table");
  return (
    <div className="flex flex-col gap-3">
      <div role="group" aria-label="View" className="flex self-end overflow-hidden rounded-md border">
        {(["drawing", "table"] as const).map((v) => (
          <Button
            key={v}
            type="button"
            size="sm"
            variant={view === v ? "secondary" : "ghost"}
            className="rounded-none"
            aria-pressed={view === v}
            onClick={() => setView(v)}
          >
            {v === "drawing" ? "Drawing" : "Table"}
          </Button>
        ))}
      </div>
      <div className={panel ? "grid gap-3 lg:grid-cols-[minmax(0,1fr)_280px]" : ""}>
        {view === "drawing" ? (
          <LargeDiagram diagram={diagram} layout={layout} facts={{ reach, related, unwritten }} selected={selected} onSelect={onSelect} />
        ) : (
          <DiagramTableView diagram={diagram} reach={reach} unwritten={unwritten} selected={selected} phone={phone} onSelect={onSelect} />
        )}
        {panel && <LargePanel diagram={diagram} reach={reach} node={node} onNavigate={onNavigate} onClose={() => onSelect(null)} />}
      </div>
    </div>
  );
}
