import { Workflow, X } from "lucide-react";
import type { ApplicationDiagram, ApplicationDiagramNode } from "@/api/portal/hooks/applications";
import { Button } from "@/components/ui/button";
import { NODE_KIND_LABEL, changeColor, flowPath, relations, type Relation } from "./diagramModel";

// DiagramPanel is what a selected node is (#2540): its name and kind, what it
// comes from and goes to, and for an automation or library the way to its
// Flow tab.

interface Props {
  diagram: ApplicationDiagram;
  node: ApplicationDiagramNode;
  onNavigate: (path: string) => void;
  onClose: () => void;
}

export function DiagramPanel({ diagram, node, onNavigate, onClose }: Props) {
  const { from, to } = relations(diagram, node.id);
  const path = flowPath(node);
  return (
    <aside
      className="flex flex-col gap-3 rounded-md border bg-card p-3 text-sm"
      aria-label={`About ${node.label}`}
      data-testid="diagram-panel"
    >
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0">
          <p className="truncate font-medium">{node.label}</p>
          <p className="text-xs text-muted-foreground">
            {NODE_KIND_LABEL[node.kind] ?? node.kind}
            {node.version ? ` · v${node.version}` : ""}
            {node.computed ? " · computed" : ""}
          </p>
        </div>
        <Button type="button" size="icon-sm" variant="ghost" aria-label="Close" onClick={onClose}>
          <X aria-hidden="true" />
        </Button>
      </div>
      {node.detail.length > 0 && (
        <ul className="space-y-0.5 text-xs text-muted-foreground">
          {node.detail.map((d) => (
            <li key={d}>{d}</li>
          ))}
        </ul>
      )}
      <RelationList title="Comes from" items={from} />
      <RelationList title="Goes to" items={to} />
      {path && (
        <Button type="button" size="sm" variant="outline" className="self-start" onClick={() => onNavigate(path)}>
          <Workflow aria-hidden="true" />
          Open flow
        </Button>
      )}
    </aside>
  );
}

function RelationList({ title, items }: { title: string; items: Relation[] }) {
  return (
    <section aria-label={title}>
      <h4 className="mb-1 text-xs font-medium text-muted-foreground">{title}</h4>
      {items.length === 0 ? (
        <p className="text-xs text-muted-foreground">Nothing</p>
      ) : (
        <ul className="space-y-1">
          {items.map((r, i) => (
            <li key={`${r.kind}-${r.other.id}-${i}`} className="flex gap-1.5">
              <span className="shrink-0 text-muted-foreground" style={{ color: changeColor(r.change) }}>
                {r.label}
              </span>
              <span className="min-w-0 truncate">{r.other.label}</span>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
