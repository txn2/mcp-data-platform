import { useEffect, useMemo, useState } from "react";
import { useApplicationDiagram, type ApplicationDetail, type ApplicationDiagram as Diagram } from "@/api/portal/hooks/applications";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { errorText } from "../applicationFormat";
import { DiagramCanvas } from "./DiagramCanvas";
import { DiagramControls } from "./DiagramControls";
import { DiagramFindings } from "./DiagramFindings";
import { layoutDiagram, type DiagramLayout } from "./diagramLayout";
import { defaultCompare, defaultVersion } from "./diagramModel";
import { DiagramPanel } from "./DiagramPanel";
import { isLarge } from "./diagramScale";
import { LargeView } from "./LargeView";

// ApplicationDiagram is the Parts card's Diagram tab (#2540): how the
// application's parts fit together at one version, drawn from what the server
// derives from their sources and settings, with what it finds wrong above it.
// Selecting a node shows what it comes from and goes to. Past LARGE_LINKS
// links between automations and tables the application is drawn as a large
// one (#2700): rows, one band, the selection's links, and a Table view.

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

interface DrawnProps {
  diagram: Diagram;
  selected: string | null;
  onSelect: (id: string | null) => void;
  onNavigate: (path: string) => void;
}

function DrawnDiagram(props: DrawnProps) {
  const large = useMemo(() => isLarge(props.diagram), [props.diagram]);
  return (
    <>
      <DiagramFindings findings={props.diagram.findings} onSelect={props.onSelect} />
      {large ? <LargeView {...props} /> : <SmallDiagram {...props} />}
    </>
  );
}

// SmallDiagram is an application drawn card by card, every link routed and
// labeled (#2540).
function SmallDiagram({ diagram, selected, onSelect, onNavigate }: DrawnProps) {
  const { layout, failed } = useDiagramLayout(diagram);
  const node = useMemo(() => diagram.nodes.find((n) => n.id === selected) ?? null, [diagram, selected]);
  return (
    <>
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
