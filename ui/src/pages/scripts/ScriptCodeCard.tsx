import { useCallback, useState, type ReactNode } from "react";
import { Card, CardAction, CardContent, CardHeader } from "@/components/ui/card";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import type { SelectedLines } from "@/lib/codemirrorLines";

// ScriptCodeCard is the script's code as views of one thing (#1906): Flow,
// the diagram the platform derives from the source, Source, the text, and
// Tests (#1972), what the version's tests found and how much of it they
// reach. Flow opens first, for owners and readers alike: it is what a person
// who did not write the script can read at a glance.
//
// Both panes stay mounted. The Source pane holds an owner's unsaved edit, and
// switching to Flow to look at something must not throw it away.
//
// The card carries what passes between the two: a card opened on Flow marks
// its lines on Source, and lines selected on Source mark their cards on Flow.

export type CodeTab = "flow" | "source" | "tests";

export interface CodeTabsLink {
  /** markedLines are the lines Flow asked Source to show. */
  markedLines: number[];
  /** selectedLines are the lines selected on Source. */
  selectedLines: SelectedLines | null;
  onSelectLines: (lines: SelectedLines | null) => void;
  /** showLines switches to Source with lines marked. */
  showLines: (lines: number[]) => void;
}

export function ScriptCodeCard({
  action,
  flow,
  source,
  tests,
}: {
  /** action is the Source tab's own controls, shown only on that tab. */
  action?: ReactNode;
  flow: (link: CodeTabsLink) => ReactNode;
  source: (link: CodeTabsLink) => ReactNode;
  /** tests is the Tests tab, drawn only while it is open: it reads the
   * version history, which nothing else on the card needs. */
  tests?: (link: CodeTabsLink) => ReactNode;
}) {
  const [tab, setTab] = useState<CodeTab>("flow");
  const [markedLines, setMarkedLines] = useState<number[]>([]);
  const [selectedLines, setSelectedLines] = useState<SelectedLines | null>(null);
  const showLines = useCallback((lines: number[]) => {
    setMarkedLines(lines);
    setTab("source");
  }, []);
  const link: CodeTabsLink = { markedLines, selectedLines, onSelectLines: setSelectedLines, showLines };

  return (
    <Card className="gap-3 py-4" data-testid="script-code">
      <Tabs value={tab} onValueChange={(v) => setTab(v as CodeTab)}>
        <CardHeader className="px-4">
          <TabsList aria-label="Script">
            <TabsTrigger value="flow">Flow</TabsTrigger>
            <TabsTrigger value="source">Source</TabsTrigger>
            {tests && <TabsTrigger value="tests">Tests</TabsTrigger>}
          </TabsList>
          {action && tab === "source" && <CardAction className="self-center">{action}</CardAction>}
        </CardHeader>
        <CardContent className="px-4">
          <TabsContent value="flow" forceMount hidden={tab !== "flow"}>
            {flow(link)}
          </TabsContent>
          <TabsContent value="source" forceMount hidden={tab !== "source"}>
            {source(link)}
          </TabsContent>
          {tests && <TabsContent value="tests">{tab === "tests" && tests(link)}</TabsContent>}
        </CardContent>
      </Tabs>
    </Card>
  );
}
