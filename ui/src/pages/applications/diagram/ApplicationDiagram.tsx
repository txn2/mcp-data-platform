import { useEffect, useMemo, useState } from "react";
import {
  useApplicationDiagram,
  type ApplicationDetail,
  type ApplicationDiagram as Diagram,
  type ApplicationFinding,
} from "@/api/portal/hooks/applications";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { errorText } from "../applicationFormat";
import { DiagramCanvas } from "./DiagramCanvas";
import { DiagramControls } from "./DiagramControls";
import { layoutDiagram, type DiagramLayout } from "./diagramLayout";
import { defaultCompare, defaultVersion } from "./diagramModel";
import { DiagramPanel } from "./DiagramPanel";

// ApplicationDiagram is the Parts card's Diagram tab (#2540): how the
// application's parts fit together at one version, drawn from what the server
// derives from their sources and settings, with what it finds wrong above it.
// Selecting a node shows what it comes from and goes to.

interface Props {
  detail: ApplicationDetail;
  onNavigate: (path: string) => void;
}

export function ApplicationDiagram({ detail, onNavigate }: Props) {
  const app = detail.application;
  const [version, setVersion] = useState(() => defaultVersion(app.current_release));
  const [comparing, setComparing] = useState(false);
  const [compareWith, setCompareWith] = useState("");
  const [selected, setSelected] = useState<string | null>(null);
  const compare = comparing && compareWith !== "" ? compareWith : null;
  const { data, isLoading, error } = useApplicationDiagram(app.id, version, compare);

  const changeVersion = (v: string) => {
    setVersion(v);
    if (comparing) setCompareWith(defaultCompare(v, app.current_release, detail.releases));
  };
  const toggleCompare = (on: boolean) => {
    setComparing(on);
    if (on && compareWith === "") setCompareWith(defaultCompare(version, app.current_release, detail.releases));
  };

  return (
    <div className="flex flex-col gap-3" data-testid="application-diagram">
      <DiagramControls
        releases={detail.releases}
        version={version}
        comparing={comparing}
        compareWith={compareWith}
        onVersion={changeVersion}
        onCompare={toggleCompare}
        onCompareWith={setCompareWith}
      />
      {error && (
        <Alert variant="destructive">
          <AlertDescription>{errorText(error, "The diagram could not be loaded")}</AlertDescription>
        </Alert>
      )}
      {isLoading && <p className="text-sm text-muted-foreground">Loading…</p>}
      {data && (
        <DrawnDiagram diagram={data} selected={selected} onSelect={setSelected} onNavigate={onNavigate} />
      )}
    </div>
  );
}

function DrawnDiagram({
  diagram,
  selected,
  onSelect,
  onNavigate,
}: {
  diagram: Diagram;
  selected: string | null;
  onSelect: (id: string | null) => void;
  onNavigate: (path: string) => void;
}) {
  const { layout, failed } = useDiagramLayout(diagram);
  const node = useMemo(() => diagram.nodes.find((n) => n.id === selected) ?? null, [diagram, selected]);
  return (
    <>
      <Findings findings={diagram.findings} onSelect={onSelect} />
      {failed && (
        <Alert variant="destructive">
          <AlertDescription>The diagram could not be drawn.</AlertDescription>
        </Alert>
      )}
      {!layout && !failed && <p className="text-sm text-muted-foreground">Drawing…</p>}
      {layout && (
        <div className={node ? "grid gap-3 lg:grid-cols-[minmax(0,1fr)_280px]" : ""}>
          <DiagramCanvas
            layout={layout}
            selected={selected}
            compared={diagram.compared_with !== undefined}
            onSelect={onSelect}
          />
          {node && (
            <DiagramPanel diagram={diagram} node={node} onNavigate={onNavigate} onClose={() => onSelect(null)} />
          )}
        </div>
      )}
    </>
  );
}

// useDiagramLayout lays a diagram out each time a new one arrives.
function useDiagramLayout(diagram: Diagram) {
  const [laid, setLaid] = useState<{ diagram: Diagram; layout: DiagramLayout } | null>(null);
  const [failed, setFailed] = useState(false);
  useEffect(() => {
    let live = true;
    setFailed(false);
    layoutDiagram(diagram).then(
      (layout) => live && setLaid({ diagram, layout }),
      () => live && setFailed(true),
    );
    return () => {
      live = false;
    };
  }, [diagram]);
  return { layout: laid?.diagram === diagram ? laid.layout : null, failed };
}

/** Findings is what the diagram finds wrong; clicking one selects the node it is about. */
function Findings({ findings, onSelect }: { findings: ApplicationFinding[]; onSelect: (id: string) => void }) {
  if (findings.length === 0) return null;
  return (
    <section aria-label="Findings" className="rounded-md border bg-muted/40 p-3" data-testid="diagram-findings">
      <h4 className="mb-1.5 text-xs font-medium text-muted-foreground">Findings</h4>
      <ul className="space-y-1.5">
        {findings.map((f, i) => (
          <li key={`${f.node}-${i}`}>
            <button
              type="button"
              className="flex w-full items-start gap-2 text-left text-sm hover:underline"
              onClick={() => onSelect(f.node)}
            >
              <span className="min-w-0 flex-1">{f.message}</span>
              {f.blocks && <Badge variant="danger">Blocks release</Badge>}
            </button>
          </li>
        ))}
      </ul>
    </section>
  );
}
