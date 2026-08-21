import { FileCode2 } from "lucide-react";
import { useAdminScripts } from "@/api/admin/hooks";
import type { Script } from "@/api/admin/types";
import { EmptyState } from "@/components/patterns/EmptyState";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Badge } from "@/components/ui/badge";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { ScriptFacetBadges } from "./ScriptFacetBadges";
import { ScriptRunsTab } from "./ScriptRunsTab";

// AdminScriptsPage is the administrator's script listing: every script on the
// platform, and what has been running (#1307).
//
// A row opens the script itself, on the same detail page its owner opens: an
// administrator runs, edits, dry-runs, schedules and reads the history of
// every script exactly as its owner does. This page lists; that page acts.

export function AdminScriptsPage({ onNavigate }: { onNavigate: (path: string) => void }) {
  const scripts = useAdminScripts();

  const allScripts = scripts.data?.data ?? [];

  return (
    <Tabs defaultValue="scripts" className="gap-4">
      {/* Two questions, two tabs: what exists, and what has been running. */}
      <TabsList
        variant="line"
        className="group-data-[orientation=horizontal]/tabs:h-auto w-full justify-start gap-1 border-b p-0"
      >
        <TabsTrigger
          value="scripts"
          className="flex-none px-4 py-2 group-data-[orientation=horizontal]/tabs:after:bottom-[-1px]"
        >
          Scripts
        </TabsTrigger>
        <TabsTrigger
          value="runs"
          className="flex-none px-4 py-2 group-data-[orientation=horizontal]/tabs:after:bottom-[-1px]"
        >
          Runs
        </TabsTrigger>
      </TabsList>

      <TabsContent value="runs">
        <ScriptRunsTab scripts={allScripts} />
      </TabsContent>

      <TabsContent value="scripts" className="space-y-4">
        {scripts.error && (
          <Alert variant="destructive">
            <AlertDescription>
              The script listing could not be loaded. The server may be unavailable.
            </AlertDescription>
          </Alert>
        )}

        <SectionCard title="All scripts">
          <ScriptsSection
            scripts={allScripts}
            isLoading={scripts.isLoading}
            onNavigate={onNavigate}
          />
        </SectionCard>
      </TabsContent>
    </Tabs>
  );
}

// ScriptsSection is the listing's three states: loading, nothing authored yet,
// and the table.
function ScriptsSection({
  scripts,
  isLoading,
  onNavigate,
}: {
  scripts: Script[];
  isLoading: boolean;
  onNavigate: (path: string) => void;
}) {
  if (isLoading) {
    return <p className="text-sm text-muted-foreground">Loading scripts...</p>;
  }
  if (scripts.length === 0) {
    return (
      <EmptyState icon={FileCode2}>
        No scripts have been authored yet. An agent creates one through the manage_script
        tool, or a person writes one on their own scripts page; it appears here as soon as
        it exists.
      </EmptyState>
    );
  }
  return <ScriptTable scripts={scripts} onNavigate={onNavigate} />;
}

function ScriptTable({
  scripts,
  onNavigate,
}: {
  scripts: Script[];
  onNavigate: (path: string) => void;
}) {
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Script</TableHead>
          <TableHead>Owner</TableHead>
          <TableHead>Visible to</TableHead>
          <TableHead>Executing</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {scripts.map((script) => (
          <TableRow
            key={script.id}
            className="cursor-pointer"
            onClick={() => onNavigate(`/admin/scripts/${script.id}`)}
          >
            <TableCell>
              <div className="font-medium">{script.display_name || script.name}</div>
              <div className="font-mono text-xs text-muted-foreground">{script.name}</div>
              <ScriptFacetBadges
                category={script.category}
                tags={script.tags}
                className="mt-1"
              />
            </TableCell>
            <TableCell className="text-xs">{script.owner_email || "—"}</TableCell>
            <TableCell className="text-xs">{audience(script)}</TableCell>
            <TableCell>
              <ExecutionState script={script} />
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

// audience renders a script's scope in the reader's terms.
function audience(script: Script): string {
  if (script.scope === "global") return "everyone";
  if (script.scope === "persona") return (script.personas ?? []).join(", ") || "no persona";
  return "its owner";
}

// ExecutionState says what the script is running, which is the only status
// that matters on this page: a saved script runs its latest version unless it
// is disabled or retired.
function ExecutionState({ script }: { script: Script }) {
  if (!script.enabled || script.status !== "active") {
    return <Badge variant="muted">{script.enabled ? script.status : "disabled"}</Badge>;
  }
  return <Badge variant="success">Runs v{script.version}</Badge>;
}
