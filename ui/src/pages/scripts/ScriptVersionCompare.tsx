import { useMemo, useState } from "react";
import { X } from "lucide-react";
import type { ScriptVersion } from "@/api/admin/types";
import { Button } from "@/components/ui/button";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { diffLines, diffStats } from "@/lib/textDiff";
import { cn } from "@/lib/utils";
import { ScriptFlowView } from "./flow/ScriptFlowView";

// ScriptVersionCompare sets an older version against the version that runs
// (#1908), two ways: Flow, the diagram of the newer version marked with what
// it reads, writes and produces that the older did not (the question a
// reviewer approving a change asks first), and Text, the line diff of the
// source. Flow opens first for the same reason the code card does.
export function ScriptVersionCompare({
  scriptId,
  from,
  to,
  onClose,
}: {
  scriptId: string;
  from: ScriptVersion;
  to: ScriptVersion;
  onClose: () => void;
}) {
  const [tab, setTab] = useState("flow");
  const { lines, stats } = useMemo(() => {
    const l = diffLines(from.source, to.source);
    return { lines: l, stats: diffStats(l) };
  }, [from.source, to.source]);

  return (
    <div data-testid="version-compare" className="space-y-3 rounded-md border p-3">
      <div className="flex items-center gap-2 text-sm">
        <span className="font-medium">
          v{from.version} → v{to.version}
        </span>
        <span className="text-xs text-emerald-600 dark:text-emerald-400">+{stats.added}</span>
        <span className="text-xs text-destructive">-{stats.removed}</span>
        <Button variant="ghost" size="icon-xs" onClick={onClose} aria-label="Close comparison" className="ml-auto">
          <X />
        </Button>
      </div>
      <Tabs value={tab} onValueChange={setTab}>
        <TabsList aria-label="Compare">
          <TabsTrigger value="flow">Flow</TabsTrigger>
          <TabsTrigger value="text">Text</TabsTrigger>
        </TabsList>
        <TabsContent value="flow">
          <ScriptFlowView
            scriptId={scriptId}
            version={to.version}
            compareWith={from.version}
            source={to.source}
            sourceSelection={null}
          />
        </TabsContent>
        <TabsContent value="text">
          <pre className="max-h-96 overflow-auto rounded-md border bg-muted/20 py-2 font-mono text-xs leading-5">
            {lines.map((l, i) => (
              <div
                key={i}
                className={cn(
                  "px-3 break-words whitespace-pre-wrap",
                  l.kind === "added" && "bg-green-500/10 text-green-600 dark:text-green-400",
                  l.kind === "removed" && "bg-red-500/10 text-red-500 dark:text-red-400",
                )}
              >
                <span className="inline-block w-4 text-muted-foreground/70 select-none">
                  {l.kind === "added" ? "+" : l.kind === "removed" ? "-" : " "}
                </span>
                {l.text}
              </div>
            ))}
          </pre>
        </TabsContent>
      </Tabs>
    </div>
  );
}
