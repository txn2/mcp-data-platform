import { useState } from "react";
import { FileCode2 } from "lucide-react";
import { useAdminScripts, useScriptReviews } from "@/api/admin/hooks";
import type { PendingReview, Script } from "@/api/admin/types";
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
import { ScriptReviewDrawer } from "./ScriptReviewDrawer";
import { ScriptReviewQueue } from "./ScriptReviewQueue";
import { ScriptRunsTab } from "./ScriptRunsTab";

// AdminScriptsPage is the administrator's script listing: the queue of versions
// waiting for a decision, every script on the platform, and what has been
// running (#1287, #1307).
//
// A row opens the script itself, on the same detail page its owner opens
// (#1367): an administrator runs, edits, dry-runs, schedules and reads the
// history of every script exactly as its owner does, and decides whether a
// version executes. This page lists; that page acts.
//
// The queue keeps its own drawer because it is a different motion — open,
// decide, next — and a reviewer working a queue should not have to walk into a
// whole script and back out again for each one.

// Open names the version the review drawer is open on.
interface Open {
  scriptID: string;
  scriptName: string;
  version: number;
}

export function AdminScriptsPage({ onNavigate }: { onNavigate: (path: string) => void }) {
  const reviews = useScriptReviews();
  const scripts = useAdminScripts();
  const [open, setOpen] = useState<Open | null>(null);

  const pending = reviews.data?.data ?? [];
  const allScripts = scripts.data?.data ?? [];

  const openReview = (row: PendingReview) =>
    setOpen({
      scriptID: row.script_id,
      scriptName: row.display_name || row.script_name,
      version: row.version,
    });

  return (
    <Tabs defaultValue="review" className="gap-4">
      {/* Two questions, two tabs: what needs a decision, and what has been
          running. The queue is first because it is the one with somebody
          waiting on it. */}
      <TabsList
        variant="line"
        className="group-data-[orientation=horizontal]/tabs:h-auto w-full justify-start gap-1 border-b p-0"
      >
        <TabsTrigger
          value="review"
          className="flex-none px-4 py-2 group-data-[orientation=horizontal]/tabs:after:bottom-[-1px]"
        >
          Review
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

      <TabsContent value="review" className="space-y-4">
        {reviews.error && (
          <Alert variant="destructive">
            <AlertDescription>
              The review queue could not be loaded, so this page cannot say whether
              anything is waiting. The server may be unavailable.
            </AlertDescription>
          </Alert>
        )}

        <ScriptReviewQueue
          pending={pending}
          isLoading={reviews.isLoading}
          selectedVersionID={openQueueRow(pending, open)?.version_id ?? null}
          onOpen={openReview}
        />

        <SectionCard title="All scripts">
          <ScriptsSection
            scripts={allScripts}
            isLoading={scripts.isLoading}
            onNavigate={onNavigate}
          />
        </SectionCard>

        {open && (
          <ScriptReviewDrawer
            key={`${open.scriptID}-${open.version}`}
            scriptID={open.scriptID}
            scriptName={open.scriptName}
            version={open.version}
            onClose={() => setOpen(null)}
          />
        )}
      </TabsContent>
    </Tabs>
  );
}

// openQueueRow finds the queue row the drawer is open on, matching the version
// as well as the script: a script can have a queued version and an older one
// open from its history, and highlighting the queued row then names a decision
// nobody is making.
function openQueueRow(pending: PendingReview[], open: Open | null): PendingReview | undefined {
  if (!open) return undefined;
  return pending.find((p) => p.script_id === open.scriptID && p.version === open.version);
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

// audience renders a script's scope in the reader's terms. It is on this table
// because scope is what decides whether a version reaches this queue at all: a
// personal script its owner wrote is approved without anybody here (#1367).
function audience(script: Script): string {
  if (script.scope === "global") return "everyone";
  if (script.scope === "persona") return (script.personas ?? []).join(", ") || "no persona";
  return "its owner";
}

// ExecutionState says what the script is running, which is the only status
// that matters on this page: a script with no approved version runs nothing,
// however healthy the rest of its record looks.
function ExecutionState({ script }: { script: Script }) {
  if (!script.approved_version_id) {
    return <Badge variant="muted">Nothing approved</Badge>;
  }
  if (!script.enabled || script.status === "deprecated") {
    return (
      <span className="flex flex-wrap items-center gap-1.5">
        <Badge variant="success">Approved</Badge>
        <Badge variant="muted">{script.enabled ? script.status : "disabled"}</Badge>
      </span>
    );
  }
  return <Badge variant="success">Approved v{script.version}</Badge>;
}
