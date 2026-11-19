import { Workflow, X } from "lucide-react";
import type { ApplicationDiagram, ApplicationDiagramNode, ApplicationNodeKind } from "@/api/portal/hooks/applications";
import { Button } from "@/components/ui/button";
import { NODE_KIND_LABEL, changeColor, flowPath, relations, type Relation } from "./diagramModel";
import { DiagramPanel } from "./DiagramPanel";
import { changeSummary, reads, writes, type Access, type Reach } from "./diagramScale";

// LargePanel is a large application's side column (#2700). With nothing
// selected it says how to read the drawing, its key and what the application
// holds, and comparing, what changed. A selected automation lists the tables
// it writes and reads; a selected table, the automations that write and read
// it. Any other part is described as on a small application.

interface Props {
  diagram: ApplicationDiagram;
  reach: Reach;
  node: ApplicationDiagramNode | null;
  onNavigate: (path: string) => void;
  onClose: () => void;
}

export function LargePanel({ diagram, reach, node, onNavigate, onClose }: Props) {
  if (!node) return <Overview diagram={diagram} />;
  if (node.kind === "script" || (node.kind === "table" && !node.computed)) {
    return <ReachPanel diagram={diagram} reach={reach} node={node} onNavigate={onNavigate} onClose={onClose} />;
  }
  return <DiagramPanel diagram={diagram} node={node} onNavigate={onNavigate} onClose={onClose} />;
}

// ReachPanel is a selected automation's tables, or a selected table's
// automations, each by how it is reached.
function ReachPanel({ diagram, reach, node, onNavigate, onClose }: Props & { node: ApplicationDiagramNode }) {
  const script = node.kind === "script";
  const m = (script ? reach.byScript : reach.byTable).get(node.id);
  const byId = new Map(diagram.nodes.map((n) => [n.id, n]));
  const names = (test: (a: Access) => boolean) =>
    [...(m ?? new Map<string, Access>())].filter(([, a]) => test(a)).map(([id]) => byId.get(id)?.label ?? id);
  const path = flowPath(node);
  return (
    <aside className="flex flex-col gap-3 rounded-md border bg-card p-3 text-sm" aria-label={`About ${node.label}`} data-testid="diagram-panel">
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0">
          <p className="truncate font-medium">{node.label}</p>
          <p className="text-xs text-muted-foreground">
            {NODE_KIND_LABEL[node.kind]}
            {node.version ? ` · v${node.version}` : ""}
          </p>
        </div>
        <Button type="button" size="icon-sm" variant="ghost" aria-label="Close" onClick={onClose}>
          <X aria-hidden="true" />
        </Button>
      </div>
      {node.detail.length > 0 && <p className="text-xs text-muted-foreground">{node.detail.join(" · ")}</p>}
      <NameList title={script ? "Writes" : "Written by"} items={names(writes)} />
      <NameList title={script ? "Reads" : "Read by"} items={names(reads)} />
      {script && <OtherLinks diagram={diagram} id={node.id} />}
      {path && (
        <Button type="button" size="sm" variant="outline" className="self-start" onClick={() => onNavigate(path)}>
          <Workflow aria-hidden="true" />
          Open flow
        </Button>
      )}
    </aside>
  );
}

// OtherLinks is what an automation comes from and goes to besides tables.
function OtherLinks({ diagram, id }: { diagram: ApplicationDiagram; id: string }) {
  const { from, to } = relations(diagram, id);
  const other = (rs: Relation[]) =>
    rs.filter((r) => r.other.kind !== "table" && r.other.kind !== "script").map((r) => `${r.label} ${r.other.label}`);
  const goes = other(to);
  return (
    <>
      <NameList title="Comes from" items={other(from)} />
      {goes.length > 0 && <NameList title="Goes to" items={goes} />}
    </>
  );
}

function NameList({ title, items }: { title: string; items: string[] }) {
  return (
    <section aria-label={title}>
      <h4 className="mb-1 text-xs font-medium text-muted-foreground">
        {title} {items.length}
      </h4>
      {items.length === 0 ? (
        <p className="text-xs text-muted-foreground">Nothing</p>
      ) : (
        <ul className="space-y-0.5">
          {items.map((i) => (
            <li key={i} className="truncate">
              {i}
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

// SUMMARY_KINDS are the kinds the overview counts, in the order it names them.
const SUMMARY_KINDS: { kind: ApplicationNodeKind; one: string; many: string }[] = [
  { kind: "source", one: "webhook source", many: "webhook sources" },
  { kind: "script", one: "automation", many: "automations" },
  { kind: "table", one: "table", many: "tables" },
  { kind: "asset", one: "asset", many: "assets" },
  { kind: "channel", one: "channel", many: "channels" },
  { kind: "output", one: "output", many: "outputs" },
];

function Overview({ diagram }: { diagram: ApplicationDiagram }) {
  const counts = SUMMARY_KINDS.map(({ kind, one, many }) => {
    const n = diagram.nodes.filter((x) => x.kind === kind && !x.computed).length;
    return n ? `${n} ${n === 1 ? one : many}` : "";
  }).filter(Boolean);
  return (
    <aside className="flex flex-col gap-3" aria-label="About the diagram" data-testid="diagram-overview">
      <div className="rounded-md border bg-card p-3 text-sm">
        <p className="mb-1 font-medium">Nothing selected</p>
        <p className="text-muted-foreground">
          Select an automation to see the tables it reads and writes, or a table to see what writes and reads it. The Table view shows every pairing at once.
        </p>
      </div>
      {diagram.compared_with !== undefined && <Changes diagram={diagram} />}
      <div className="flex flex-col gap-2 rounded-md border bg-card p-3 text-xs text-muted-foreground" data-testid="diagram-key">
        <p className="text-sm font-medium text-foreground">Key</p>
        <span className="flex items-center gap-2">
          <i className="inline-block h-[3px] w-7 rounded-sm bg-muted-foreground" />
          writes (thick line)
        </span>
        <span className="flex items-center gap-2">
          <i className="inline-block h-px w-7 bg-muted-foreground" />
          reads (thin line)
        </span>
        <span className="flex items-center gap-2">
          <i className="inline-block w-7 border-t-2 border-dashed border-muted-foreground" />
          named only when it runs
        </span>
        <span className="flex items-center gap-2">
          <i className="inline-block h-2 w-7 rounded bg-muted-foreground/30" />
          automations to tables, all
        </span>
      </div>
      <p className="rounded-md border bg-card p-3 text-xs leading-5 text-muted-foreground">
        {counts.join(" · ")}
        <br />
        {diagram.findings.length === 0 ? "No findings" : `${diagram.findings.length} findings`}
      </p>
    </aside>
  );
}

function Changes({ diagram }: { diagram: ApplicationDiagram }) {
  const s = changeSummary(diagram);
  const groups = [
    { title: "Added", change: "added" as const, nodes: s.added },
    { title: "Changed", change: "changed" as const, nodes: s.changed },
    { title: "Removed", change: "removed" as const, nodes: s.removed },
  ];
  return (
    <div className="flex flex-col gap-2 rounded-md border bg-card p-3 text-sm" data-testid="diagram-changes">
      <p className="font-medium">Changes since release {diagram.compared_with}</p>
      <p className="text-xs text-muted-foreground">
        {s.added.length} added · {s.changed.length} changed · {s.removed.length} removed
      </p>
      {groups
        .filter((g) => g.nodes.length > 0)
        .map((g) => (
          <section key={g.title} aria-label={g.title}>
            <h4 className="text-xs font-medium" style={{ color: changeColor(g.change) }}>
              {g.title}
            </h4>
            <ul className="text-xs">
              {g.nodes.map((n) => (
                <li key={n.id}>
                  {n.label} <span className="text-muted-foreground">{changeDetail(n)}</span>
                </li>
              ))}
            </ul>
          </section>
        ))}
      <p className="text-xs text-muted-foreground">Links that were added or removed are drawn without a selection.</p>
    </div>
  );
}

function changeDetail(n: ApplicationDiagramNode): string {
  if (n.change === "changed") return `v${n.was_version ?? 0} → v${n.version ?? 0}`;
  return (NODE_KIND_LABEL[n.kind] ?? n.kind).toLowerCase();
}
